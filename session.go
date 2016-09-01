package smux

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

const (
	defaultAcceptBacklog = 1024
	defaultCloseWait     = 1024
)

const (
	errBrokenPipe      = "broken pipe"
	errInvalidProtocol = "invalid protocol version"
)

// Session defines a multiplexed connection for streams
type Session struct {
	// connection related
	conn io.ReadWriteCloser

	// stream related
	nextStreamID uint32
	streams      map[uint32]*Stream
	frameSize    uint16
	rdEvents     map[uint32]chan struct{}

	// token controlled read buffer
	tbf         chan struct{}
	streamLines map[uint32][]Frame

	//
	die       chan struct{}
	chAccepts chan *Stream
	chClose   chan uint32
	dataReady int32
	mu        sync.Mutex
}

func newSession(conn io.ReadWriteCloser, client bool, maxframes int, framesize uint16) *Session {
	s := new(Session)
	s.conn = conn
	s.frameSize = framesize
	s.streams = make(map[uint32]*Stream)
	s.tbf = make(chan struct{}, maxframes)
	s.streamLines = make(map[uint32][]Frame)
	s.rdEvents = make(map[uint32]chan struct{})
	s.chAccepts = make(chan *Stream, defaultAcceptBacklog)
	s.chClose = make(chan uint32, defaultCloseWait)
	s.die = make(chan struct{})
	for i := 0; i < maxframes; i++ {
		s.tbf <- struct{}{}
	}
	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 2
	}
	go s.recvLoop()
	go s.monitor()
	go s.keepalive()
	return s
}

// OpenStream is used to create a new stream
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
	s.sendFrame(newFrame(cmdSYN, sid))
	return stream, nil
}

// AcceptStream is used to block until the next available stream
// is ready to be accepted.
func (s *Session) AcceptStream() (*Stream, error) {
	select {
	case stream := <-s.chAccepts:
		return stream, nil
	case <-s.die:
		return nil, errors.New(errBrokenPipe)
	}
}

// Close is used to close the session and all streams.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.die:
		return errors.New(errBrokenPipe)
	default:
		close(s.die)
		for k := range s.streams {
			s.streams[k].Close()
		}
		s.sendFrame(newFrame(cmdTerminate, 0))
		s.conn.Close()
	}
	return nil
}

// NumStreams returns the number of currently open streams
func (s *Session) NumStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.streams)
}

func (s *Session) streamClose(sid uint32) {
	s.chClose <- sid
}

// nonblocking read from session pool, for streams
func (s *Session) nioread(sid uint32) *Frame {
	s.mu.Lock()
	frames := s.streamLines[sid]
	if len(frames) > 0 {
		f := frames[0]
		s.streamLines[sid] = frames[1:]
		s.tbf <- struct{}{}
		s.mu.Unlock()
		return &f
	}
	s.mu.Unlock()
	return nil
}

// session read a frame from underlying connection
func (s *Session) readFrame(buffer []byte) (f Frame, err error) {
	if _, err := io.ReadFull(s.conn, buffer[:headerSize]); err != nil {
		return f, errors.Wrap(err, "readFrame")
	}

	dec := rawHeader(buffer)
	if dec.Version() != version {
		return f, errors.New(errInvalidProtocol)
	}

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

// monitors streams
func (s *Session) monitor() {
	for {
		select {
		case sid := <-s.chClose:
			s.mu.Lock()
			delete(s.streams, sid)
			delete(s.rdEvents, sid)
			ntokens := len(s.streamLines[sid])
			delete(s.streamLines, sid)
			s.mu.Unlock()
			for i := 0; i < ntokens; i++ { // return stream tokens to the pool
				s.tbf <- struct{}{}
			}
		case <-s.die:
			return
		}
	}
}

// recvLoop keeps on reading from underlying connection if tokens are available
func (s *Session) recvLoop() {
	buffer := make([]byte, (1<<16)+headerSize)
	for {
		select {
		case <-s.tbf:
			if f, err := s.readFrame(buffer); err == nil {
				s.mu.Lock()
				switch f.cmd {
				case cmdNOP:
					s.tbf <- struct{}{}
				case cmdTerminate:
					s.Close()
					return
				case cmdSYN:
					chNotifyReader := make(chan struct{}, 1)
					s.streams[f.sid] = newStream(f.sid, s.frameSize, chNotifyReader, s)
					s.rdEvents[f.sid] = chNotifyReader
					s.chAccepts <- s.streams[f.sid]
					s.tbf <- struct{}{}
				default:
					if _, ok := s.streams[f.sid]; ok {
						s.streamLines[f.sid] = append(s.streamLines[f.sid], f)
						select {
						case s.rdEvents[f.sid] <- struct{}{}:
						default:
						}
					} else {
						s.tbf <- struct{}{}
					}
				}
				s.mu.Unlock()
				atomic.StoreInt32(&s.dataReady, 1)
			} else {
				s.Close()
				return
			}
		case <-s.die:
			return
		}
	}
}

func (s *Session) keepalive() {
	tickerPing := time.NewTicker(10 * time.Second)
	tickerTimeout := time.NewTicker(20 * time.Second)
	defer tickerPing.Stop()
	defer tickerTimeout.Stop()
	for {
		select {
		case <-tickerPing.C:
			s.sendFrame(newFrame(cmdNOP, 0))
		case <-tickerTimeout.C:
			if !atomic.CompareAndSwapInt32(&s.dataReady, 1, 0) {
				s.Close()
				return
			}
		case <-s.die:
			return
		}
	}
}

func (s *Session) sendFrame(f Frame) {
	bts, _ := f.MarshalBinary()
	s.conn.Write(bts)
}
