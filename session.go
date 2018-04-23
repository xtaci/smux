package smux

import (
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"container/heap"
)

const (
	defaultAcceptBacklog = 1024
)

const (
	errBrokenPipe      = "broken pipe"
	errInvalidProtocol = "invalid protocol version"
	errGoAway          = "stream id overflows, should start a new connection"
)

type writeRequest struct {
	niceness uint8
	sequence uint64 // Used to keep the heap ordered by time
	frame    Frame
	result   chan writeResult
}

type writeResult struct {
	n   int
	err error
}

type writeHeap []writeRequest

func (h writeHeap) Len() int { return len(h) }
func (h writeHeap) Less(i, j int) bool {
	if h[i].niceness == h[j].niceness {
		return h[i].sequence < h[j].sequence
	}
	return h[i].niceness < h[j].niceness
}
func (h writeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *writeHeap) Push(x interface{}) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*h = append(*h, x.(writeRequest))
}

func (h *writeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Session defines a multiplexed connection for streams
type Session struct {
	conn io.ReadWriteCloser

	config           *Config
	nextStreamID     uint32 // next stream identifier
	nextStreamIDLock sync.Mutex

	streams    map[uint32]*Stream // all streams in this session
	streamLock sync.Mutex         // locks streams

	die       chan struct{} // flag session has died
	dieLock   sync.Mutex
	chAccepts chan *Stream

	dataReady int32 // flag data has arrived

	goAway int32 // flag id exhausted

	deadline atomic.Value

	writeTicket      chan struct{}
	writesLock       sync.Mutex
	writes           writeHeap
	writeSequenceNum uint64
}

func newSession(config *Config, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.die = make(chan struct{})
	s.conn = conn
	s.config = config
	s.streams = make(map[uint32]*Stream)
	s.chAccepts = make(chan *Stream, defaultAcceptBacklog)
	s.writeTicket = make(chan struct{})

	if client {
		s.nextStreamID = 1
	} else {
		s.nextStreamID = 0
	}
	go s.recvLoop()
	go s.sendLoop()
	go s.keepalive()
	return s
}

func (s *Session) OpenStream() (*Stream, error) {
	return s.OpenStreamOpt(100)
}

// OpenStream is used to create a new stream
func (s *Session) OpenStreamOpt(niceness uint8) (*Stream, error) {
	if s.IsClosed() {
		return nil, errors.New(errBrokenPipe)
	}

	// generate stream id
	s.nextStreamIDLock.Lock()
	if s.goAway > 0 {
		s.nextStreamIDLock.Unlock()
		return nil, errors.New(errGoAway)
	}

	s.nextStreamID += 2
	sid := s.nextStreamID
	if sid == sid%2 { // stream-id overflows
		s.goAway = 1
		s.nextStreamIDLock.Unlock()
		return nil, errors.New(errGoAway)
	}
	s.nextStreamIDLock.Unlock()

	stream := newStream(sid, niceness, s.config.MaxFrameSize, int32(s.config.MaxPerStreamReceiveBuffer), s)

	if _, err := s.writeFrame(0, newFrame(cmdSYN, sid)); err != nil {
		return nil, errors.Wrap(err, "writeFrame")
	}

	s.streamLock.Lock()
	s.streams[sid] = stream
	s.streamLock.Unlock()
	return stream, nil
}

func (s *Session) AcceptStream() (*Stream, error) {
	return s.AcceptStreamOpt(100)
}

// AcceptStream is used to block until the next available stream
// is ready to be accepted.
func (s *Session) AcceptStreamOpt(niceness uint8) (*Stream, error) {
	var deadline <-chan time.Time
	if d, ok := s.deadline.Load().(time.Time); ok && !d.IsZero() {
		timer := time.NewTimer(time.Until(d))
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case stream := <-s.chAccepts:
		stream.niceness = niceness
		return stream, nil
	case <-deadline:
		return nil, errTimeout
	case <-s.die:
		return nil, errors.New(errBrokenPipe)
	}
}

// Close is used to close the session and all streams.
func (s *Session) Close() (err error) {
	s.dieLock.Lock()

	select {
	case <-s.die:
		s.dieLock.Unlock()
		return errors.New(errBrokenPipe)
	default:
		close(s.die)
		s.dieLock.Unlock()
		s.streamLock.Lock()
		for k := range s.streams {
			s.streams[k].sessionClose()
		}
		s.streamLock.Unlock()
		return s.conn.Close()
	}
}

// IsClosed does a safe check to see if we have shutdown
func (s *Session) IsClosed() bool {
	select {
	case <-s.die:
		return true
	default:
		return false
	}
}

// NumStreams returns the number of currently open streams
func (s *Session) NumStreams() int {
	if s.IsClosed() {
		return 0
	}
	s.streamLock.Lock()
	defer s.streamLock.Unlock()
	return len(s.streams)
}

// SetDeadline sets a deadline used by Accept* calls.
// A zero time value disables the deadline.
func (s *Session) SetDeadline(t time.Time) error {
	s.deadline.Store(t)
	return nil
}

// notify the session that a stream has closed
func (s *Session) streamClosed(sid uint32) {
	s.streamLock.Lock()
	delete(s.streams, sid)
	s.streamLock.Unlock()
}

func (s *Session) queueAcks(streamId uint32, n int32) {
	ack := newFrame(cmdACK, streamId)
	ack.data = make([]byte, 4)
	binary.BigEndian.PutUint32(ack.data, uint32(n))
	s.queueFrame(0, ack)
}

// session read a frame from underlying connection
// it's data is pointed to the input buffer
func (s *Session) readFrame(buffer []byte) (f Frame, err error) {
	if _, err := io.ReadFull(s.conn, buffer[:headerSize]); err != nil {
		return f, errors.Wrap(err, "readFrame")
	}

	dec := rawHeader(buffer)
	if dec.Version() != version {
		return f, errors.New(errInvalidProtocol)
	}

	f.ver = dec.Version()
	f.cmd = dec.Cmd()
	f.sid = dec.StreamID()
	if length := dec.Length(); length > 0 {
		if _, err := io.ReadFull(s.conn, buffer[headerSize:headerSize+length]); err != nil {
			return f, errors.Wrap(err, "readFrame")
		}
		f.data = buffer[headerSize : headerSize+length]
	}
	return f, nil
}

// recvLoop keeps on reading from underlying connection if tokens are available
func (s *Session) recvLoop() {
	buffer := make([]byte, (1<<16)+headerSize)
	for {
		if f, err := s.readFrame(buffer); err == nil {
			atomic.StoreInt32(&s.dataReady, 1)

			switch f.cmd {
			case cmdNOP:
			case cmdSYN:
				s.streamLock.Lock()
				if _, ok := s.streams[f.sid]; !ok {
					stream := newStream(f.sid, 255, s.config.MaxFrameSize, int32(s.config.MaxPerStreamReceiveBuffer), s)
					s.streams[f.sid] = stream
					select {
					case s.chAccepts <- stream:
					case <-s.die:
					}
				}
				s.streamLock.Unlock()
			case cmdFIN:
				s.streamLock.Lock()
				if stream, ok := s.streams[f.sid]; ok {
					stream.markRST()
					stream.notifyReadEvent()
				}
				s.streamLock.Unlock()
			case cmdPSH:
				s.streamLock.Lock()
				if stream, ok := s.streams[f.sid]; ok {
					stream.pushBytes(f.data)
					stream.notifyReadEvent()
				}
				s.streamLock.Unlock()
			case cmdACK:
				s.streamLock.Lock()
				if stream, ok := s.streams[f.sid]; ok {
					tokens := binary.BigEndian.Uint32(f.data)
					stream.receiveAck(int32(tokens))
				}
				s.streamLock.Unlock()
			default:
				s.Close()
				return
			}
		} else {
			s.Close()
			return
		}
	}
}

func (s *Session) keepalive() {
	tickerPing := time.NewTicker(s.config.KeepAliveInterval)
	tickerTimeout := time.NewTicker(s.config.KeepAliveTimeout)
	defer tickerPing.Stop()
	defer tickerTimeout.Stop()
	for {
		select {
		case <-tickerPing.C:
			s.writeFrame(0, newFrame(cmdNOP, 0))
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

func (s *Session) sendLoop() {
	buf := make([]byte, (1<<16)+headerSize)
	for {
		select {
		case <-s.die:
			return
		case <-s.writeTicket:
			s.writesLock.Lock()
			request := heap.Pop(&s.writes).(writeRequest)
			s.writesLock.Unlock()

			buf[0] = request.frame.ver
			buf[1] = request.frame.cmd
			binary.LittleEndian.PutUint16(buf[2:], uint16(len(request.frame.data)))
			binary.LittleEndian.PutUint32(buf[4:], request.frame.sid)
			copy(buf[headerSize:], request.frame.data)
			n, err := s.conn.Write(buf[:headerSize+len(request.frame.data)])

			n -= headerSize
			if n < 0 {
				n = 0
			}

			result := writeResult{
				n:   n,
				err: err,
			}

			request.result <- result
			close(request.result)
		}
	}
}

func (s *Session) queueFrame(niceness uint8, f Frame) (writeRequest, error) {
	req := writeRequest{
		niceness: niceness,
		sequence: atomic.AddUint64(&s.writeSequenceNum, 1),
		frame:    f,
		result:   make(chan writeResult, 1),
	}
	s.writesLock.Lock()
	heap.Push(&s.writes, req)
	s.writesLock.Unlock()
	select {
	case <-s.die:
		return req, errors.New(errBrokenPipe)
	case s.writeTicket <- struct{}{}:
	}
	return req, nil
}

// writeFrame writes the frame to the underlying connection
// and returns the number of bytes written if successful
func (s *Session) writeFrame(niceness uint8, f Frame) (n int, err error) {
	req, err := s.queueFrame(niceness, f)
	if err != nil {
		return 0, err
	}
	result := <-req.result
	return result.n, result.err
}
