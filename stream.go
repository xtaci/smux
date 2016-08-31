package smux

import (
	"bytes"
	"io"
	"sync"

	"github.com/pkg/errors"
)

// Stream implements io.ReadWriteCloser
type Stream struct {
	id             uint32
	chNotifyReader chan struct{}
	sess           *Session
	frameSize      uint16
	die            chan struct{}
	rlock          sync.Mutex
	buffer         []byte
}

func newStream(id uint32, frameSize uint16, chNotifyReader chan struct{}, sess *Session) *Stream {
	s := new(Stream)
	s.id = id
	s.chNotifyReader = chNotifyReader
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
		return 0, errors.New("broken pipe")
	default:
	}

	s.rlock.Lock()
	if len(s.buffer) > 0 {
		n = copy(b, s.buffer)
		s.buffer = s.buffer[n:]
		return n, nil
	}

	if f := s.sess.nioread(s.id); f != nil {
		switch f.cmd {
		case cmdPSH:
			n = copy(b, f.data)
			if len(f.data) > n {
				s.buffer = make([]byte, len(f.data)-n)
				copy(s.buffer, f.data[n:])
			}
			s.rlock.Unlock()
			return n, nil
		default:
			s.rlock.Unlock()
			return 0, io.EOF
		}
	}

	s.rlock.Unlock()
	select {
	case <-s.chNotifyReader:
		goto READ
	case <-s.die:
		return 0, errors.New("broken pipe")
	}
}

// Write implements io.ReadWriteCloser
func (s *Stream) Write(b []byte) (n int, err error) {
	select {
	case <-s.die:
		return 0, errors.New("broken pipe")
	default:
	}

	frames := s.split(b, cmdPSH, s.id)
	var combined bytes.Buffer
	for k := range frames {
		bts, _ := frames[k].MarshalBinary()
		combined.Write(bts)
	}

	if _, err = s.sess.lw.Write(combined.Bytes()); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close implements io.ReadWriteCloser
func (s *Stream) Close() error {
	select {
	case <-s.die:
		return errors.New("broken pipe")
	default:
		close(s.die)
		s.sess.streamClose(s.id)
		f := newFrame(cmdRST, s.id)
		bts, _ := f.MarshalBinary()
		s.sess.lw.Write(bts)
	}
	return nil
}

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
