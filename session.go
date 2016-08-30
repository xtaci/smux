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

type bufferPool struct {
	reader      io.Reader
	tokens      chan struct{}
	streamLines map[uint32][]Frame
	events      map[uint32]chan struct{}
	total       uint32
	die         chan struct{}
	mu          sync.Mutex
}

func newBufferPool(reader io.Reader, tokens uint32) *bufferPool {
	bp := new(bufferPool)
	bp.tokens = make(chan struct{}, tokens)
	bp.streamLines = make(map[uint32][]Frame)
	bp.events = make(map[uint32]chan struct{})
	bp.die = make(chan struct{})
	go bp.recvLoop()
	return bp
}

func (bp *bufferPool) register(sid uint32, ch chan struct{}) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.events[sid] = ch
}

// nonblocking read from bufferPool
func (bp *bufferPool) read(sid uint32) *Frame {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.streamLines[sid]) > 0 {
		f := bp.streamLines[sid][0]
		bp.streamLines[sid] = bp.streamLines[sid][1:]
		return &f
	}
	return nil
}

// read a frame from underlying connection
func (bp *bufferPool) readFrame() (f Frame, err error) {
	h := make([]byte, headerSize)
	if _, err := io.ReadFull(bp.reader, h); err != nil {
		return f, errors.Wrap(err, "readFrame")
	}

	dec := RawHeader(h)
	data := h
	if dec.Length() > 0 {
		data = make([]byte, headerSize+dec.Length())
		copy(data, h)
		if _, err := io.ReadFull(bp.reader, data[headerSize:]); err != nil {
			return f, errors.Wrap(err, "readFrame")
		}
	}
	return Unmarshal(data), nil
}

func (bp *bufferPool) recvLoop() {
	for {
		select {
		case <-bp.tokens:
			if f, err := bp.readFrame(); err == nil {
				switch f.cmd {
				case cmdSYN:
				case cmdACK:
				case cmdFIN:
				case cmdRST:
				case cmdPSH:
				default:
					log.Println("frame command unknown:", f.cmd)
				}
			} else {
				bp.close()
			}
		case <-bp.die:
			return
		}
	}
}

func (bp *bufferPool) close() {
	select {
	case <-bp.die:
	default:
		close(bp.die)
	}
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
