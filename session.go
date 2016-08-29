package smux

import (
	"io"
	"net"
	"sync"

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
	qdisc        Qdisc
	mu           sync.Mutex
}

func newSession(config *Config, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.config = config
	s.conn = conn
	s.qdisc = newFIFOQdisc()
	s.streams = make(map[uint32]*Stream)

	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.monitor()
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream() (*Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := newStream(s.nextStreamID, new(DefaultFramer), s.qdisc)
	s.nextStreamID += 2
	s.streams[stream.id] = stream
	return stream, nil
}

// Open is used to create a new stream as a net.Conn
func (s *Session) Open() (net.Conn, error) {
	conn, err := s.OpenStream()
	if err != nil {
		return nil, err
	}
	return conn, nil
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
	h := make([]byte, headerSize)
	for {
		io.ReadFull(s.conn, h)
		dec := header(h)
		data := h
		if dec.Length() > 0 {
			data = make([]byte, headerSize+dec.Length())
			copy(data, h)
			io.ReadFull(s.conn, data[headerSize:])
		}
		s.demux(data)
	}
}

// demux the slice into the corresponding stream
func (s *Session) demux(bts []byte) {
	frame := Deserialize(bts)
	switch frame.cmd {
	case cmdSYN, cmdACK, cmdRST:
		s.streams[frame.sid].chRx <- *frame
	}
}
