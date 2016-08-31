package smux

import (
	"io"
	"sync"

	"github.com/pkg/errors"
)

const (
	defaultAcceptBacklog = 1024
	defaultCloseWait     = 1024
)

// Session defines a multiplexed connection for streams
type Session struct {
	// connection related
	conn io.ReadWriteCloser
	lw   *lockedWriter

	// stream related
	nextStreamID uint32
	streams      map[uint32]*Stream
	frameSize    int

	// rx pool control
	tokens      chan struct{}
	streamLines map[uint32][]Frame
	rdEvents    map[uint32]chan struct{}

	// control
	die       chan struct{}
	chAccepts chan *Stream
	chClose   chan uint32
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

func newSession(conn io.ReadWriteCloser, client bool, maxframes, framesize int) *Session {
	s := new(Session)
	s.conn = conn
	s.frameSize = framesize
	s.lw = &lockedWriter{conn: conn}
	s.streams = make(map[uint32]*Stream)
	s.tokens = make(chan struct{}, maxframes)
	s.streamLines = make(map[uint32][]Frame)
	s.rdEvents = make(map[uint32]chan struct{})
	s.chAccepts = make(chan *Stream, defaultAcceptBacklog)
	s.chClose = make(chan uint32, defaultCloseWait)
	s.die = make(chan struct{})
	for i := 0; i < maxframes; i++ {
		s.tokens <- struct{}{}
	}
	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.recvLoop()
	go s.monitor()
	return s
}

// OpenStream opens a stream on the connection
func (s *Session) OpenStream() (*Stream, error) {
	// track stream
	s.mu.Lock()
	sid := s.nextStreamID
	s.nextStreamID += 2
	chNotifyReader := make(chan struct{}, 1)
	stream := newStream(sid, s.frameSize, chNotifyReader, s)
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

// Close is used to close the session and all streams.
func (s *Session) Close() error {
	select {
	case <-s.die:
		return errors.New("broken pipe")
	default:
		close(s.die)
		s.mu.Lock()
		defer s.mu.Unlock()
		for k := range s.streams {
			s.streams[k].Close()
		}
	}
	return nil
}

func (s *Session) streamClose(sid uint32) {
	s.chClose <- sid
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
func (s *Session) readFrame(buffer []byte) (f Frame, err error) {
	if _, err := io.ReadFull(s.conn, buffer[:headerSize]); err != nil {
		return f, errors.Wrap(err, "readFrame")
	}

	dec := rawHeader(buffer)
	if length := dec.Length(); length > 0 {
		if _, err := io.ReadFull(s.conn, buffer[headerSize:headerSize+length]); err != nil {
			return f, errors.Wrap(err, "readFrame")
		}
		f.UnmarshalBinary(buffer[:headerSize+length])
		return f, nil
	}
	f.UnmarshalBinary(buffer[:headerSize])
	return f, nil
}

func (s *Session) monitor() {
	for {
		select {
		case sid := <-s.chClose:
			s.mu.Lock()
			delete(s.streams, sid)
			delete(s.rdEvents, sid)
			delete(s.streamLines, sid)
			s.mu.Unlock()
		case <-s.die:
			return
		}
	}
}

// recvLoop keeps on reading from underlying connection if tokens are available
func (s *Session) recvLoop() {
	buffer := make([]byte, s.frameSize+headerSize)
	for {
		select {
		case <-s.tokens:
			if f, err := s.readFrame(buffer); err == nil {
				s.mu.Lock()
				if _, ok := s.streams[f.sid]; ok {
					s.streamLines[f.sid] = append(s.streamLines[f.sid], f)
					select {
					case s.rdEvents[f.sid] <- struct{}{}:
					default:
					}
				} else if f.cmd == cmdSYN {
					chNotifyReader := make(chan struct{}, 1)
					s.streams[f.sid] = newStream(f.sid, s.frameSize, chNotifyReader, s)
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
