package smux

import (
	"bytes"
	"log"
	"net"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const (
	streamIdle = 1 << iota
	streamSynSent
	streamEstablished
	streamClosed
)

const (
	streamRxQueueLimit = 8192
)

// Stream implements io.ReadWriteCloser
type Stream struct {
	id               uint32
	state            int
	buffer           bytes.Buffer
	chRx             chan Frame
	fr               Framer
	qdisc            Qdisc
	mu               sync.Mutex
	die              chan struct{}
	chNotifyReadable chan struct{}
	readDeadline     time.Time
	writeDeadline    time.Time
}

func newStream(id uint32, fr Framer, qdisc Qdisc) *Stream {
	s := new(Stream)
	s.id = id
	s.fr = fr
	s.qdisc = qdisc
	s.state = streamIdle
	s.chRx = make(chan Frame, streamRxQueueLimit)
	s.die = make(chan struct{})
	s.chNotifyReadable = make(chan struct{}, 1)
	go s.monitor()
	return s
}

// Read implements io.ReadWriteCloser
func (s *Stream) Read(b []byte) (n int, err error) {
	if s.buffer.Len() > 0 {
		return s.buffer.Read(b)
	}
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
		frames[0].cmd = cmdSYN
		s.state = streamSynSent
	}

	// TODO: block write
	for k := range frames {
		s.qdisc.Enqueue(frames[k])
	}

	return 0, nil
}

// Close implements io.ReadWriteCloser
func (s *Stream) Close() error {
	select {
	case <-s.die:
		return errors.New("broken pipe")
	default:
		s.state = streamClosed
		close(s.die)
	}
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

// SetDeadline sets the read and write deadlines
func (s *Stream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	if err := s.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readDeadline = t
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline = t
	return nil
}

// stream monitor
func (s *Stream) monitor() {
	for {
		select {
		case f := <-s.chRx:
			switch s.state {
			case streamEstablished:
				if f.cmd == cmdRST {
					s.state = streamClosed
					log.Println("connection reset")
				} else {
					if n, err := s.buffer.Write(f.payload); err != nil {
						log.Println(n, err)
					}
					s.notifyReadable()
				}
			case streamSynSent:
				if f.cmd == cmdACK {
					s.state = streamEstablished
					if n, err := s.buffer.Write(f.payload); err != nil {
						log.Println(n, err)
					}
					s.notifyReadable()
				} else {
					s.state = streamClosed
					log.Println("incorrect packet", f.cmd)
				}
			}
		case <-s.die:
			return
		}
	}
}

func (s *Stream) notifyReadable() {
	select {
	case s.chNotifyReadable <- struct{}{}:
	default:
	}
}
