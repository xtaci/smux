package smux

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

// Stream implements net.Conn
type Stream struct {
	id            uint32
	rstflag       int32
	sess          *Session
	buffer        *bytes.Buffer
	bufferLock    sync.Mutex
	frameSize     int
	chReadEvent   chan struct{} // notify a read event
	die           chan struct{} // flag the stream has closed
	dieLock       sync.Mutex
	readDeadline  atomic.Value
	writeDeadline atomic.Value
}

// newStream initiates a Stream struct
func newStream(id uint32, frameSize int, sess *Session) *Stream {
	s := new(Stream)
	s.id = id
	s.chReadEvent = make(chan struct{}, 1)
	s.frameSize = frameSize
	s.sess = sess
	s.die = make(chan struct{})
	s.buffer = new(bytes.Buffer)
	return s
}

// ID returns the unique stream ID.
func (s *Stream) ID() uint32 {
	return s.id
}

// Read implements net.Conn
func (s *Stream) Read(b []byte) (n int, err error) {
	if len(b) == 0 {
		select {
		case <-s.die:
			return 0, errors.New(errBrokenPipe)
		default:
			return 0, nil
		}
	}

	var deadline <-chan time.Time
	if d, ok := s.readDeadline.Load().(time.Time); ok && !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}

READ:
	s.bufferLock.Lock()
	n, _ = s.buffer.Read(b)
	s.bufferLock.Unlock()

	if n > 0 {
		s.sess.returnTokens(n)
		return n, nil
	} else if atomic.LoadInt32(&s.rstflag) == 1 {
		_ = s.Close()
		return 0, io.EOF
	}

	select {
	case <-s.chReadEvent:
		goto READ
	case <-deadline:
		return n, errTimeout
	case <-s.die:
		return 0, errors.New(errBrokenPipe)
	}
}

// Write implements net.Conn
func (s *Stream) Write(b []byte) (n int, err error) {
	var deadline <-chan time.Time
	if d, ok := s.writeDeadline.Load().(time.Time); ok && !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case <-s.die:
		return 0, errors.New(errBrokenPipe)
	default:
	}

	// frame split and transmit
	sent := 0
	frame := newFrame(cmdPSH, s.id)
	bts := b
	for len(bts) > 0 {
		sz := len(bts)
		if sz > s.frameSize {
			sz = s.frameSize
		}
		frame.data = bts[:sz]
		bts = bts[sz:]
		n, err := s.sess.writeFrameInternal(frame, deadline)
		sent += n
		if err != nil {
			return sent, err
		}
	}

	return sent, nil
}

// Close implements net.Conn
func (s *Stream) Close() error {
	s.dieLock.Lock()

	select {
	case <-s.die:
		s.dieLock.Unlock()
		return errors.New(errBrokenPipe)
	default:
		close(s.die)
		s.dieLock.Unlock()
		s.sess.streamClosed(s.id)
		_, err := s.sess.writeFrame(newFrame(cmdFIN, s.id))
		return err
	}
}

// GetDieCh returns a readonly chan which can be readable
// when the stream is to be closed.
func (s *Stream) GetDieCh() <-chan struct{} {
	return s.die
}

// SetReadDeadline sets the read deadline as defined by
// net.Conn.SetReadDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readDeadline.Store(t)
	return nil
}

// SetWriteDeadline sets the write deadline as defined by
// net.Conn.SetWriteDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.Store(t)
	return nil
}

// SetDeadline sets both read and write deadlines as defined by
// net.Conn.SetDeadline.
// A zero time value disables the deadlines.
func (s *Stream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	if err := s.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// session closes the stream
func (s *Stream) sessionClose() {
	s.dieLock.Lock()
	defer s.dieLock.Unlock()

	select {
	case <-s.die:
	default:
		close(s.die)
	}
}

// LocalAddr satisfies net.Conn interface
func (s *Stream) LocalAddr() net.Addr {
	if ts, ok := s.sess.conn.(interface {
		LocalAddr() net.Addr
	}); ok {
		return ts.LocalAddr()
	}
	return nil
}

// RemoteAddr satisfies net.Conn interface
func (s *Stream) RemoteAddr() net.Addr {
	if ts, ok := s.sess.conn.(interface {
		RemoteAddr() net.Addr
	}); ok {
		return ts.RemoteAddr()
	}
	return nil
}

// receiveBytes receive from the reader and write into the buffer
func (s *Stream) receiveBytes(r io.Reader, sz int64) (written int64, err error) {
	s.bufferLock.Lock()
	written, err = io.CopyN(s.buffer, r, sz)
	s.bufferLock.Unlock()
	return
}

// recycleTokens transform remaining bytes to tokens(will truncate buffer)
func (s *Stream) recycleTokens() (n int) {
	s.bufferLock.Lock()
	n = s.buffer.Len()
	s.buffer.Reset()
	s.bufferLock.Unlock()
	return
}

// notify read event
func (s *Stream) notifyReadEvent() {
	select {
	case s.chReadEvent <- struct{}{}:
	default:
	}
}

// mark this stream has been reset
func (s *Stream) markRST() {
	atomic.StoreInt32(&s.rstflag, 1)
}

var errTimeout error = &timeoutError{}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
