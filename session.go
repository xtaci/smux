package smux

import (
	"io"
	"log"
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
	lw               *lockedWriter
	muConn           sync.Mutex
	nextStreamID     uint32
	streams          map[uint32]*Stream
	die              chan struct{}
	chAccept         chan *Stream
	mu               sync.Mutex
	remoteWindowSize uint32
	maxWindowSize    uint32
	curWindowSize    uint32
}

type lockedWriter struct {
	mu   sync.Mutex
	conn io.Writer
}

func (lw *lockedWriter) Write(p []byte) (n int, err error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.conn.Write(p)
}

func newSession(maxWindowSize uint32, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.conn = conn
	s.lw = &lockedWriter{conn: conn}
	s.maxWindowSize = maxWindowSize
	s.remoteWindowSize = initRemoteWindowSize
	s.streams = make(map[uint32]*Stream)

	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.demux()
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream() (*Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := newStream(s.nextStreamID, defaultFrameSize, s.lw)
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
		dec := RawHeader(h)
		data := h
		if dec.Length() > 0 {
			data = make([]byte, headerSize+dec.Length())
			copy(data, h)
			io.ReadFull(s.conn, data[headerSize:])
		}
		frame := Unmarshal(data)
		switch frame.cmd {
		case cmdSYN:
		case cmdACK:
		case cmdFIN:
		case cmdRST:
		case cmdPSH:
			s.streams[frame.sid].chRx <- frame
		default:
			log.Println("frame command unknown:", frame.cmd)
		}
	}
}
