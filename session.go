package smux

import (
	"io"
	"sync"

	"github.com/pkg/errors"
)

const (
	defaultFrameSize     = 4096
	defaultAcceptBacklog = 1024
)

type Session struct {
	// connection related
	conn   io.ReadWriteCloser
	lw     *lockedWriter
	muConn sync.Mutex

	// stream related
	nextStreamID uint32
	streams      map[uint32]*Stream

	// rx pool control
	tokens      chan struct{}
	streamLines map[uint32][]Frame
	rdEvents    map[uint32]chan struct{}

	// session control
	die       chan struct{}
	chAccepts chan *Stream
	mu        sync.Mutex
}

type lockedWriter struct {
	mu   sync.Mutex
	conn io.Writer
}

func (lw *lockedWriter) Write(p []byte) (n int, err error) {
	lw.mu.Lock()
	n, err = lw.conn.Write(p)
	lw.mu.Unlock()
	return
}

func newSession(maxframes uint32, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.conn = conn
	s.lw = &lockedWriter{conn: conn}
	s.streams = make(map[uint32]*Stream)
	s.tokens = make(chan struct{}, maxframes)
	s.streamLines = make(map[uint32][]Frame)
	s.rdEvents = make(map[uint32]chan struct{})
	s.chAccepts = make(chan *Stream, defaultAcceptBacklog)
	s.die = make(chan struct{})
	for i := uint32(0); i < maxframes; i++ {
		s.tokens <- struct{}{}
	}
	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.recvLoop()
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream() (*Stream, error) {
	// track stream
	s.mu.Lock()
	sid := s.nextStreamID
	s.nextStreamID += 2
	chNotifyReader := make(chan struct{}, 1)
	stream := newStream(sid, defaultFrameSize, chNotifyReader, s)
	s.rdEvents[sid] = chNotifyReader
	s.streams[sid] = stream
	s.mu.Unlock()

	// send SYN packet
	f := newFrame(cmdSYN, sid)
	bts, _ := f.MarshalBinary()
	s.lw.Write(bts)
	return stream, nil
}

// AcceptStream is used to block until the next available stream
// is ready to be accepted.
func (s *Session) AcceptStream() (*Stream, error) {
	select {
	case stream := <-s.chAccepts:
		return stream, nil
	case <-s.die:
		return nil, errors.New("session shutdown")
	}
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.streams {
		s.streams[k].Close()
	}
	return nil
}

func (s *Session) streamClose(sid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, sid)
	delete(s.rdEvents, sid)
	delete(s.streamLines, sid)
}

// nonblocking frame read for a session
func (s *Session) nioread(sid uint32) *Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames := s.streamLines[sid]
	if len(frames) > 0 {
		f := frames[0]
		s.streamLines[sid] = frames[1:]
		s.tokens <- struct{}{}
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

	dec := rawHeader(h)
	data := h
	if dec.Length() > 0 {
		data = make([]byte, headerSize+dec.Length())
		copy(data, h)
		if _, err := io.ReadFull(s.conn, data[headerSize:]); err != nil {
			return f, errors.Wrap(err, "readFrame")
		}
	}
	err = f.UnmarshalBinary(data)
	return f, err
}

// recvLoop keeps on reading from underlying connection if tokens are available
func (s *Session) recvLoop() {
	for {
		select {
		case <-s.tokens:
			if f, err := s.readFrame(); err == nil {
				s.mu.Lock()
				if _, ok := s.streams[f.sid]; ok {
					s.streamLines[f.sid] = append(s.streamLines[f.sid], f)
					select {
					case s.rdEvents[f.sid] <- struct{}{}:
					default:
					}
				} else if f.cmd == cmdSYN {
					chNotifyReader := make(chan struct{}, 1)
					s.streams[f.sid] = newStream(f.sid, defaultFrameSize, chNotifyReader, s)
					s.rdEvents[f.sid] = chNotifyReader
					s.chAccepts <- s.streams[f.sid]
					s.tokens <- struct{}{}
				}
				s.mu.Unlock()
			} else {
				return
			}
		case <-s.die:
			return
		}
	}
}
