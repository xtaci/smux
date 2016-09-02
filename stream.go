package smux

import (
	"bytes"
	"sync"

	"github.com/pkg/errors"
)

// Stream implements io.ReadWriteCloser
type Stream struct {
	id          uint32
	sess        *Session
	frameSize   int
	rlock       sync.Mutex    // read lock
	buffer      []byte        // temporary store of remaining frame.data
	chReadEvent chan struct{} // notify a read event
	die         chan struct{} // flag the stream has closed
	dieLock     sync.Mutex
}

// newStream initiates a Stream struct
func newStream(id uint32, frameSize int, sess *Session) *Stream {
	s := new(Stream)
	s.id = id
	s.chReadEvent = make(chan struct{}, 1)
	s.frameSize = frameSize
	s.sess = sess
	s.die = make(chan struct{})
	return s
}

// Read implements io.ReadWriteCloser
func (s *Stream) Read(b []byte) (n int, err error) {
READ:
	select {
	case <-s.die:
		return 0, errors.New(errBrokenPipe)
	default:
	}

	s.rlock.Lock()
	if len(s.buffer) > 0 {
		n = copy(b, s.buffer)
		s.buffer = s.buffer[n:]
		s.rlock.Unlock()
		return n, nil
	}

	if f := s.sess.nioread(s.id); f != nil && f.cmd == cmdPSH {
		n = copy(b, f.data)
		if len(f.data) > n {
			s.buffer = make([]byte, len(f.data)-n)
			copy(s.buffer, f.data[n:])
		}
		s.rlock.Unlock()
		return n, nil
	}

	s.rlock.Unlock()
	select {
	case <-s.chReadEvent:
		goto READ
	case <-s.die:
		return 0, errors.New(errBrokenPipe)
	}
}

// Write implements io.ReadWriteCloser
func (s *Stream) Write(b []byte) (n int, err error) {
	select {
	case <-s.die:
		return 0, errors.New(errBrokenPipe)
	default:
	}

	frames := s.split(b, cmdPSH, s.id)
	// combine the frames
	var buffer bytes.Buffer
	for k := range frames {
		bts, _ := frames[k].MarshalBinary()
		buffer.Write(bts)
	}

	if _, err = s.sess.writeBinary(buffer.Bytes()); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close implements io.ReadWriteCloser
func (s *Stream) Close() error {
	s.dieLock.Lock()
	defer s.dieLock.Unlock()

	select {
	case <-s.die:
		return errors.New(errBrokenPipe)
	default:
		close(s.die)
		s.sess.streamClosed(s.id)
		s.sess.writeFrame(newFrame(cmdRST, s.id))
	}
	return nil
}

// split large byte buffer into smaller frames
func (s *Stream) split(bts []byte, cmd byte, sid uint32) []Frame {
	var frames []Frame
	for len(bts) > int(s.frameSize) {
		frame := newFrame(cmd, sid)
		frame.data = make([]byte, s.frameSize)
		n := copy(frame.data, bts)
		bts = bts[n:]
		frames = append(frames, frame)
	}
	if len(bts) > 0 {
		frame := newFrame(cmd, sid)
		frame.data = make([]byte, len(bts))
		copy(frame.data, bts)
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
