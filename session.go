package smux

import (
	"io"
	"net"
	"sync"

	"github.com/pkg/errors"
)

const (
	defaultFrameSize     = 65536
	rxQueueLimit         = 8192
	initRemoteWindowSize = 65535
)

type Session struct {
	conn             io.ReadWriteCloser
	nextStreamID     uint32
	streams          map[uint32]*Stream
	die              chan struct{}
	chAccept         chan *Stream
	qdisc            Qdisc
	mu               sync.Mutex
	framer           *Framer
	remoteWindowSize uint32
	maxWindowSize    uint32
	curWindowSize    uint32
}

func newSession(maxWindowSize uint32, qdisc Qdisc, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.conn = conn
	s.framer = newFramer(defaultFrameSize)
	s.qdisc = newFIFOQdisc()
	s.maxWindowSize = maxWindowSize
	s.remoteWindowSize = initRemoteWindowSize
	s.streams = make(map[uint32]*Stream)

	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.mux()
	go s.demux()
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream() (*Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := newStream(s.nextStreamID, s.framer, s.qdisc)
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

// demux
func (s *Session) demux() {
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
		frame := Deserialize(data)
		switch frame.cmd {
		case cmdSYN:
		case cmdACK:
		case cmdRST:
			s.streams[frame.sid].chRx <- *frame
		}
		s.remoteWindowSize = frame.wnd
	}
}

// mux
func (s *Session) mux() {
	for {
		f := s.qdisc.Dequeue()
		f.wnd = s.curWindowSize
	}
}
