package smux

import (
	"io"
	"net"

	"github.com/pkg/errors"
)

const (
	rxQueueLimit = 8192
)

type Session struct {
	conn         io.ReadWriteCloser
	nextStreamID uint32
	config       *Config
	streams      map[uint32]*Stream
	die          chan struct{}
	chAccept     chan *Stream
}

func newSession(config *Config, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.config = config
	s.conn = conn
	s.streams = make(map[uint32]*Stream)
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream(*Stream, error) {
}

// Accept is used to block until the next available stream
// is ready to be accepted.
func (s *Session) Accept() (net.Conn, error) {
	conn, err := s.AcceptStream()
	if err != nil {
		return nil, err
	}
	return conn, err
}

// AcceptStream is used to block until the next available stream
// is ready to be accepted.
func (s *Session) AcceptStream() (*Stream, error) {
	select {
	case stream := <-s.chAccept:
		return stream, nil
	case <-s.die:
		return nil, errors.New("session shutdown")
	}
}

// demux the slice into the corresponding stream
func (s *Session) demux(bts []byte) {
}

// monitor the session
func (s *Session) monitor() {
	ch := make(chan []byte, rxQueueLimit)
	go s.recvLoop(ch)
	for {
		select {
		case bts := <-ch:
			s.demux(bts)
		case <-s.die:
			return
		}
	}
}

// recvLoop continuous read packets from conn
func (s *Session) recvLoop(ch chan []byte) {
	for {
	}
}
