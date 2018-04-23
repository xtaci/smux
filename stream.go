package smux

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"container/heap"
)

// Stream implements net.Conn
type Stream struct {
	id                     uint32
	niceness               uint8
	rstflag                int32
	sess                   *Session
	buffer                 bytes.Buffer
	bufferLock             sync.Mutex
	frameSize              int
	chReadEvent            chan struct{} // notify a read event
	die                    chan struct{} // flag the stream has closed
	dieLock                sync.Mutex
	readDeadline           atomic.Value
	writeDeadline          atomic.Value
	writeTokenBucket       int32         // write tokens required for writing to the sessions
	writeTokenBucketNotify chan struct{} // used for waiting for tokens
}

// newStream initiates a Stream struct
func newStream(id uint32, niceness uint8, frameSize int, writeTokenBucketSize int32, sess *Session) *Stream {
	s := new(Stream)
	s.niceness = niceness
	s.id = id
	s.chReadEvent = make(chan struct{}, 1)
	s.frameSize = frameSize
	s.sess = sess
	s.die = make(chan struct{})
	s.writeTokenBucket = writeTokenBucketSize
	s.writeTokenBucketNotify = make(chan struct{}, 1)
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
	n, err = s.buffer.Read(b)
	s.bufferLock.Unlock()

	if n > 0 {
		s.sess.queueAcks(s.id, int32(n))
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

	frames := s.split(b, cmdPSH, s.id)
	sent := 0
	for k := range frames {
		for atomic.LoadInt32(&s.writeTokenBucket) <= 0 {
			select {
			case <-s.writeTokenBucketNotify:
			case <-s.die:
				return sent, errors.New(errBrokenPipe)
			case <-deadline:
				return sent, errTimeout
			}
		}

		atomic.AddInt32(&s.writeTokenBucket, -int32(len(frames[k].data)))

		req := writeRequest{
			niceness: s.niceness,
			sequence: atomic.AddUint64(&s.sess.writeSequenceNum, 1),
			frame:    frames[k],
			result:   make(chan writeResult, 1),
		}

		// TODO(jnewman): replace with session.writeFrame(..)?
		s.sess.writesLock.Lock()
		heap.Push(&s.sess.writes, req)
		s.sess.writesLock.Unlock()
		select {
		case s.sess.writeTicket <- struct{}{}:
		case <-s.die:
			return sent, errors.New(errBrokenPipe)
		case <-deadline:
			return sent, errTimeout
		}

		select {
		case result := <-req.result:
			sent += result.n
			if result.err != nil {
				return sent, result.err
			}
		case <-s.die:
			return sent, errors.New(errBrokenPipe)
		case <-deadline:
			return sent, errTimeout
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
		_, err := s.sess.writeFrame(0, newFrame(cmdFIN, s.id))
		return err
	}
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

// pushBytes a slice into buffer
func (s *Stream) pushBytes(p []byte) {
	s.bufferLock.Lock()
	s.buffer.Write(p)
	s.bufferLock.Unlock()
}

// receiveAck replenishes the token writeTokenBucket so that more writes can proceed
func (s *Stream) receiveAck(numTokens int32) {
	if atomic.AddInt32(&s.writeTokenBucket, numTokens) > 0 {
		s.notifyBucket()
	}
}

// notifyBucket notifies waiting write loops that there are more tokens in the writeTokenBucket
func (s *Stream) notifyBucket() {
	select {
	case s.writeTokenBucketNotify <- struct{}{}:
	default:
	}
}

// split large byte buffer into smaller frames, reference only
func (s *Stream) split(bts []byte, cmd byte, sid uint32) []Frame {
	frames := make([]Frame, 0, len(bts)/s.frameSize+1)
	for len(bts) > s.frameSize {
		frame := newFrame(cmd, sid)
		frame.data = bts[:s.frameSize]
		bts = bts[s.frameSize:]
		frames = append(frames, frame)
	}
	if len(bts) > 0 {
		frame := newFrame(cmd, sid)
		frame.data = bts
		frames = append(frames, frame)
	}
	return frames
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
