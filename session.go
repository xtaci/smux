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

	// pool control
	tokens      chan struct{}
	streamLines map[uint32][]Frame
	events      map[uint32]chan struct{}
	total       uint32
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

func newSession(maxframes uint32, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.conn = conn
	s.lw = &lockedWriter{conn: conn}
	s.remoteWindowSize = initRemoteWindowSize
	s.streams = make(map[uint32]*Stream)
	s.tokens = make(chan struct{}, maxframes)
	s.streamLines = make(map[uint32][]Frame)
	s.events = make(map[uint32]chan struct{})
	s.die = make(chan struct{})
	for i := uint32(0); i < maxframes; i++ {
		s.tokens <- struct{}{}
	}
	go s.recvLoop()

	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
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

// nonblocking frame read for a session
func (s *Session) read(sid uint32) *Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.streamLines[sid]) > 0 {
		f := s.streamLines[sid][0]
		s.streamLines[sid] = s.streamLines[sid][1:]
		return &f
	}
	return nil
}

// read a frame from underlying connection
func (s *Session) readFrame() (f Frame, err error) {
	h := make([]byte, headerSize)
	if _, err := io.ReadFull(s.conn, h); err != nil {
		return f, errors.Wrap(err, "readFrame")
	}

	dec := RawHeader(h)
	data := h
	if dec.Length() > 0 {
		data = make([]byte, headerSize+dec.Length())
		copy(data, h)
		if _, err := io.ReadFull(s.conn, data[headerSize:]); err != nil {
			return f, errors.Wrap(err, "readFrame")
		}
	}
	return Unmarshal(data), nil
}

func (s *Session) recvLoop() {
	for {
		select {
		case <-s.tokens:
			if f, err := s.readFrame(); err == nil {
				switch f.cmd {
				case cmdSYN:
					s.returntoken()
				case cmdACK:
					s.returntoken()
				case cmdFIN:
					s.returntoken()
				case cmdRST:
					s.returntoken()
				case cmdPSH:
				default:
					log.Println("frame command unknown:", f.cmd)
					s.returntoken()
				}
			}
		case <-s.die:
			return
		}
	}
}

func (s *Session) returntoken() {
	s.tokens <- struct{}{}
}
