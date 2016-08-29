package smux

import (
	"errors"
	"net"
	"sync"
	"time"
)

const (
	streamIdle = 1 << iota
	streamNew
	streamEstablished
	streamClosed
)

// Stream implements io.ReadWriteCloser
type Stream struct {
	id      uint32
	state   int
	rxQueue []Frame // receive queue
	chRx    chan Frame
	fr      Framer
	qdisc   Qdisc
	mu      sync.Mutex
	die     chan struct{}
}

func newStream(id uint32, fr Framer, qdisc Qdisc) *Stream {
	s := new(Stream)
	s.id = id
	s.fr = fr
	s.qdisc = qdisc
	s.state = streamIdle
	s.chRx = make(chan Frame, 8192)
	s.die = make(chan struct{})
	go s.monitor()
	return s
}

// Read implements io.ReadWriteCloser
func (s *Stream) Read(b []byte) (n int, err error) {
	return 0, nil
}

// Write implements io.ReadWriteCloser
func (s *Stream) Write(b []byte) (n int, err error) {
	frames := s.fr.Split(b)
	if len(frames) == 0 {
		return 0, errors.New("cannot split frame")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case streamIdle:
		frames[0].options |= flagSYN
		s.state = streamNew
	}

	// TODO: block write
	for k := range frames {
		s.qdisc.Enqueue(frames[k])
	}

	return 0, nil
}

// Close implements io.ReadWriteCloser
func (s *Stream) Close() error {
	return nil
}

// LocalAddr is used to get the local address of the
// underlying connection.
func (s *Stream) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr is used to get the address of remote end
// of the underlying connection
func (s *Stream) RemoteAddr() net.Addr {
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
func (s *Stream) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
func (s *Stream) SetWriteDeadline(t time.Time) error {
	return nil
}

// SetDeadline sets the read and write deadlines
func (s *Stream) SetDeadline(t time.Time) error {
	return nil
}

// stream monitor
func (s *Stream) monitor() {
	for {
		select {
		case f := <-s.chRx:
		}
	}
}
