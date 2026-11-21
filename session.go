// MIT License
//
// Copyright (c) 2016-2017 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package smux

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/transport/v5/deadline"
	"github.com/puzpuzpuz/xsync/v4"
	libsync "github.com/xtaci/smux/internal/lib/sync"
)

const (
	defaultAcceptBacklog = 1024
	maxShaperSize        = 1024
	openCloseTimeout     = 30 * time.Second // Timeout for opening/closing streams
)

// resultChanPool reduces allocation of result channels
var resultChanPool = sync.Pool{
	New: func() any {
		return make(chan writeResult, 1)
	},
}

// CLASSID identifies a frame's scheduling class.
type CLASSID int

const (
	CLSCTRL CLASSID = iota // prioritized control signal
	CLSDATA
)

// timeoutError represents a timeout during an operation such as accept, read, or write.
//
// To better cooperate with the standard library, timeoutError should implement the standard library's `net.Error`.
//
// For example, using smux to implement net.Listener and work with http.Server, the keep-alive connection (*smux.Stream) will be unexpectedly closed.
// For more details, see https://github.com/xtaci/smux/pull/99.
type timeoutError struct{}

// Error returns the timeout error message.
func (timeoutError) Error() string { return "timeout" }

// Temporary reports that the timeout is temporary.
func (timeoutError) Temporary() bool { return true }

// Timeout reports that the error was caused by a timeout.
func (timeoutError) Timeout() bool { return true }

var (
	ErrInvalidProtocol           = errors.New("invalid protocol")
	ErrConsumed                  = errors.New("peer consumed more than sent")
	ErrGoAway                    = errors.New("stream id overflows, should start a new connection")
	ErrTimeout         net.Error = &timeoutError{}
	ErrWouldBlock                = errors.New("operation would block on IO")
)

// writeRequest represents a frame waiting to be written by sendLoop.
type writeRequest struct {
	class  CLASSID
	frame  Frame
	seq    uint32
	result chan writeResult
}

// writeResult records the payload bytes written and any resulting error.
type writeResult struct {
	n   int
	err error
}

// Session manages streams multiplexed over a single connection.
type Session struct {
	conn io.ReadWriteCloser

	config           *Config
	goAway           int32  // flag id exhausted
	nextStreamID     uint32 // next stream identifier
	nextStreamIDLock sync.Mutex

	bucket       atomic.Int32  // shared receive-buffer token count
	bucketNotify chan struct{} // used for waiting for tokens

	streams *xsync.Map[uint32, *stream] // all streams in this session

	done   chan struct{} // closed when session shutdown begins
	closed atomic.Bool   // fast IsClosed check

	// socket state
	active                  atomic.Bool // inbound activity since the previous timeout check
	readErr, writeErr       error
	readClosed, writeClosed atomic.Bool
	readDone, writeDone     chan struct{}

	acceptCh       chan *stream
	acceptDeadline *deadline.Deadline // deadline for Accept()

	activeCall     *libsync.Quit     // tracks in-flight write requests during shutdown
	requestID      atomic.Uint32     // Monotonic increasing write request ID
	shaper         chan writeRequest // a shaper for writing
	sq             *shaperQueue
	shaperDone     chan struct{}
	shaperPending  chan struct{}
	shaperConsumed chan struct{}
}

// newSession creates a session and starts its background processing loops.
func newSession(config *Config, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.done = make(chan struct{})
	s.conn = conn
	s.config = config
	s.streams = xsync.NewMap[uint32, *stream]()
	s.acceptCh = make(chan *stream, defaultAcceptBacklog)
	s.bucket.Store(int32(config.MaxReceiveBuffer))
	s.bucketNotify = make(chan struct{}, 1)
	s.activeCall = libsync.NewQuit()
	s.shaper = make(chan writeRequest, maxShaperSize)
	s.readDone = make(chan struct{})
	s.writeDone = make(chan struct{})
	s.shaperPending = make(chan struct{}, 1)
	s.shaperConsumed = make(chan struct{}, 1)
	s.shaperDone = make(chan struct{})
	s.sq = NewShaperQueue()

	s.nextStreamID = 0
	if client {
		s.nextStreamID = 1
	}

	s.acceptDeadline = deadline.New()
	// Release the accept-deadline timer when the session is collected.
	runtime.AddCleanup(s, freeDeadline, s.acceptDeadline)

	go s.shaperLoop()
	go s.recvLoop()
	go s.sendLoop()
	if !config.KeepAliveDisabled {
		go s.timerLoop()
	}
	return s
}

// allocStreamID allocates the next stream ID, or reports exhaustion.
func (s *Session) allocStreamID() (uint32, bool) {
	s.nextStreamIDLock.Lock()
	defer s.nextStreamIDLock.Unlock()

	if s.goAway > 0 {
		return 0, false
	}
	// Detect wraparound before advancing to the next stream ID.
	if s.nextStreamID+2 < s.nextStreamID {
		s.goAway = 1
		return 0, false
	}
	// Reserve the next ID in this endpoint's sequence.
	s.nextStreamID += 2
	return s.nextStreamID, true
}

// OpenStream opens a new stream on the session.
func (s *Session) OpenStream() (*Stream, error) {
	if s.IsClosed() {
		return nil, io.ErrClosedPipe
	}

	if s.writeClosed.Load() {
		return nil, s.writeErr
	}

	if s.readClosed.Load() {
		return nil, s.readErr
	}

	sid, ok := s.allocStreamID()
	if !ok {
		return nil, ErrGoAway
	}

	stream := newStream(sid, s.config.MaxFrameSize, s)

	// Register the stream before sending SYN so an immediate peer response is
	// not discarded by recvLoop as belonging to an unknown stream.
	s.streams.Store(sid, stream)

	_, err := s.writeControlFrame(newFrame(byte(s.config.Version), cmdSYN, sid))
	if err != nil {
		goto cleanup
	}

	select {
	case <-s.done:
	case <-s.writeDone:
	case <-s.readDone:
	default:
		wrapper := &Stream{stream: stream}
		wrapper.finalizer = runtime.AddCleanup(wrapper, freeStream, stream)
		return wrapper, nil
	}

	// A terminal signal may race with the successful SYN write. Resolve the
	// result from published session state, preferring session close, then
	// write failure, then read failure, rather than from the select case chosen.
	switch {
	case s.IsClosed():
		err = io.ErrClosedPipe
	case s.writeClosed.Load():
		err = s.writeErr
	case s.readClosed.Load():
		err = s.readErr
	default:
		panic("unexpected state received a signal")
	}

cleanup:
	s.streamClosed(sid)
	return nil, err
}

// Open opens a stream and returns it as an io.ReadWriteCloser.
func (s *Session) Open() (io.ReadWriteCloser, error) {
	return s.OpenStream()
}

// AcceptStream waits for and returns the next incoming stream.
func (s *Session) AcceptStream() (*Stream, error) {
	if s.IsClosed() {
		return nil, io.ErrClosedPipe
	}

	if s.acceptDeadline.Err() != nil {
		return nil, ErrTimeout
	}

	// Arrival of a stream may race with a terminal read error or session close.
	// If terminal state is already published when dequeuing, return that error
	// instead of handing out a stream.
	select {
	case stream := <-s.acceptCh:
		if s.IsClosed() {
			return nil, io.ErrClosedPipe
		}

		if s.readClosed.Load() {
			return nil, s.readErr
		}

		wrapper := &Stream{stream: stream}
		wrapper.finalizer = runtime.AddCleanup(wrapper, freeStream, stream)
		return wrapper, nil
	case <-s.readDone:
		if s.IsClosed() {
			return nil, io.ErrClosedPipe
		}

		return nil, s.readErr
	case <-s.acceptDeadline.Done():
		if s.IsClosed() {
			return nil, io.ErrClosedPipe
		}

		if s.readClosed.Load() {
			return nil, s.readErr
		}

		return nil, ErrTimeout
	}
}

// Accept returns the next stream as a generic io.ReadWriteCloser.
func (s *Session) Accept() (io.ReadWriteCloser, error) {
	return s.AcceptStream()
}

// closeStreams closes every registered stream and reclaims its receive tokens.
func (s *Session) closeStreams() {
	s.streams.DeleteMatching(func(_ uint32, stream *stream) (bool, bool) {
		stream.sessionClose()
		if n := stream.recycleTokens(); n > 0 {
			s.bucket.Add(int32(n))
		}
		return true, false
	})
}

// Close is used to close the session and all streams.
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return io.ErrClosedPipe
	}

	close(s.done)
	// Closing the connection interrupts recvLoop/sendLoop if they are blocked
	// in connection I/O; wait for those loops and shaperLoop before reclaiming
	// streams and their receive buffers.
	err := s.conn.Close()

	// Wait for the receive, send, and shaper loops to finish their shutdown.
	<-s.readDone
	<-s.writeDone
	<-s.shaperDone

	s.closeStreams()
	return err
}

// CloseChan returns a channel that is closed when the session shuts down.
func (s *Session) CloseChan() <-chan struct{} {
	return s.done
}

// notifyBucket wakes recvLoop after receive-buffer capacity becomes available.
func (s *Session) notifyBucket() {
	select {
	case s.bucketNotify <- struct{}{}:
	default:
	}
}

// IsClosed reports whether the session has shut down.
func (s *Session) IsClosed() bool {
	return s.closed.Load()
}

// NumStreams returns the number of streams currently registered in the session.
func (s *Session) NumStreams() int {
	if s.IsClosed() {
		return 0
	}
	return s.streams.Size()
}

// SetDeadline sets a deadline used by Accept* calls.
// A zero time value disables the deadline.
func (s *Session) SetDeadline(t time.Time) error {
	s.acceptDeadline.Set(t)
	return nil
}

// LocalAddr returns the underlying connection's local address, if available.
func (s *Session) LocalAddr() net.Addr {
	if ts, ok := s.conn.(interface {
		LocalAddr() net.Addr
	}); ok {
		return ts.LocalAddr()
	}
	return nil
}

// RemoteAddr returns the underlying connection's remote address, if available.
func (s *Session) RemoteAddr() net.Addr {
	if ts, ok := s.conn.(interface {
		RemoteAddr() net.Addr
	}); ok {
		return ts.RemoteAddr()
	}
	return nil
}

// streamClosed removes a closed stream and returns its unused receive tokens.
func (s *Session) streamClosed(sid uint32) {
	stream, ok := s.streams.LoadAndDelete(sid)
	if !ok {
		return
	}

	n := stream.recycleTokens()
	if n <= 0 {
		return
	}

	// Restore receive capacity held by unread data from this stream.
	if s.bucket.Add(int32(n)) > 0 {
		s.notifyBucket()
	}
}

// returnTokens returns receive-buffer capacity after a stream reads data.
func (s *Session) returnTokens(n int) {
	if s.bucket.Add(int32(n)) > 0 {
		s.notifyBucket()
	}
}

func (s *Session) readRecord() error {
	var hdr rawHeader

	// Read the next frame header while receive capacity is available.
	_, err := io.ReadFull(s.conn, hdr[:])
	if err != nil {
		return err
	}

	// Refresh activity for keep-alive timeout detection.
	s.active.Store(true)

	// Reject frames from an incompatible protocol version.
	if hdr.Version() != byte(s.config.Version) {
		return ErrInvalidProtocol
	}

	// Dispatch the frame according to its command.
	sid := hdr.StreamID()
	switch hdr.Cmd() {
	case cmdNOP:
		if hdr.Length() != 0 {
			return ErrInvalidProtocol
		}

		return nil
	case cmdSYN:
		// Open a new peer-initiated stream.

		if hdr.Length() != 0 {
			return ErrInvalidProtocol
		}

		if s.IsClosed() {
			return io.ErrClosedPipe
		}

		stream, loaded := s.streams.LoadOrCompute(sid, func() (*stream, bool) {
			return newStream(sid, s.config.MaxFrameSize, s), false
		})
		if loaded {
			// Ignore duplicate SYN frames; do not enqueue the same stream twice.
			return nil
		}

		// Do not hold a stream-map lock while sending: a full accept queue may
		// block recvLoop, but must not prevent other stream operations.
		select {
		case s.acceptCh <- stream:
		case <-s.done:
			return io.ErrClosedPipe
		}
		return nil

	case cmdFIN:
		// Mark the peer's write side as closed.

		if hdr.Length() != 0 {
			return ErrInvalidProtocol
		}

		stream, ok := s.streams.Load(sid)
		if !ok {
			return nil
		}

		// Wake stream operations waiting for peer data or closure.
		stream.fin()
		return nil

	case cmdPSH:
		// Read and queue stream payload data.

		if hdr.Length() == 0 {
			return nil
		}

		pNewbuf := malloc(int(hdr.Length()))
		// Read the complete payload before dispatching the frame.
		written, err := io.ReadFull(s.conn, *pNewbuf)
		if err != nil {
			free(pNewbuf)
			return err
		}

		// Serialize lookup, the post-FIN check, and enqueueing with stream
		// removal: data is queued only while this stream remains registered.
		s.streams.Compute(sid, func(stream *stream, ok bool) (*stream, xsync.ComputeOp) {
			if !ok {
				// Discarded payloads consume no shared receive-buffer tokens.
				free(pNewbuf)
				return nil, xsync.CancelOp
			}

			// Ignore data sent after the peer closed its write side.
			if stream.isReadClosed.Load() {
				// Data after FIN is discarded and consumes no receive tokens.
				free(pNewbuf)
				return nil, xsync.CancelOp
			}

			stream.pushBytes(pNewbuf)
			// Tokens are charged only while payload is buffered; reads
			// and stream cleanup return them to the shared bucket.
			s.bucket.Add(-int32(written))
			stream.wakeupReader()
			return nil, xsync.CancelOp
		})
		return nil

	case cmdUPD:
		// Apply a protocol-version-2 flow-control update.
		if s.config.Version != 2 {
			return ErrInvalidProtocol
		}

		if hdr.Length() != szCmdUPD {
			return ErrInvalidProtocol
		}

		var hdr updHeader
		_, err := io.ReadFull(s.conn, hdr[:])
		if err != nil {
			return err
		}

		// Apply the update if the referenced stream is still registered.
		stream, ok := s.streams.Load(sid)
		if !ok {
			return nil
		}

		stream.update(hdr.Consumed(), hdr.Window())
		return nil

	default:
		return ErrInvalidProtocol
	}
}

// recvLoop reads and dispatches frames while receive-buffer capacity is available.
func (s *Session) recvLoop() {
	var err error
RECV_LOOP:
	for {
		if s.IsClosed() {
			break RECV_LOOP
		}

		// Wait for receive capacity, or stop if the session closes.
		for s.bucket.Load() <= 0 {
			select {
			case <-s.done:
				// With no receive capacity, shutdown wakes this wait directly.
				// A shutdown during io.ReadFull instead surfaces as a read error.
				break RECV_LOOP
			case <-s.bucketNotify:
				// If the session is closed, stop waiting for receive capacity.
				if s.IsClosed() {
					break RECV_LOOP
				}
			}
		}

		err = s.readRecord()
		if err != nil {
			break RECV_LOOP
		}
	}

	if s.IsClosed() {
		err = io.ErrClosedPipe
	}
	// Publish the terminal error before closing readDone so waiters can safely
	// read it after they wake.
	s.readErr = err
	s.readClosed.Store(true)
	close(s.readDone)

	// No more SYN frames can arrive after this loop exits. Discard streams that
	// were queued for acceptance but had not yet been received by the caller.
	for {
		select {
		case <-s.acceptCh:
		default:
			return
		}
	}
}

// keepalive sends periodic NOP frames and closes an inactive session on timeout.
func (s *Session) timerLoop() {
	ping := time.NewTicker(s.config.KeepAliveInterval)
	defer ping.Stop()

	timeout := time.NewTicker(s.config.KeepAliveTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-ping.C:
			writeFrameInternal(s, newFrame(byte(s.config.Version), cmdNOP, 0), ping.C, CLSCTRL)
			s.notifyBucket() // Wake recvLoop in case it is waiting for capacity.
		case <-timeout.C:
			if s.active.CompareAndSwap(true, false) {
				// Inbound traffic during the interval indicates peer activity.
				continue
			}

			// Do not time out while recvLoop is paused because the shared
			// receive buffer is full.
			if s.bucket.Load() <= 0 {
				// recvLoop is paused by backpressure and cannot observe activity.
				continue
			}

			s.Close()
			return
		case <-s.done:
			return
		}
	}
}

// shaperLoop prioritizes control frames and schedules data frames fairly across streams.
func (s *Session) shaperLoop() {
	shaper := s.shaper

SHAPER_LOOP:
	for {
		select {
		case <-s.done:
			break SHAPER_LOOP
		case <-s.writeDone:
			break SHAPER_LOOP
		case r := <-shaper:
			s.sq.Push(r)
			// Batch additional requests while there is room in the shaper queue.
			for len(shaper) > 0 && s.sq.Len() < maxShaperSize {
				select {
				case r := <-shaper:
					s.sq.Push(r)
				default:
				}
			}
			// Wake sendLoop to process the queued requests.
			s.notifyShaperPending()

			if s.sq.Len() >= maxShaperSize {
				// Pause admissions until sendLoop frees queue capacity.
				shaper = nil
			}
		case <-s.shaperConsumed:
			// Resume accepting requests after the queue has been consumed.
			shaper = s.shaper
		}
	}

	// Stop admitting writes and wait for existing callers to leave before
	// recycling their result channels from the pending queues.
	s.activeCall.Wait()
	s.discardPendingWrites()
	close(s.shaperDone)
}

// discardPendingWrites returns writeResult channels to the pool after discarding pending write requests.
// It is called only after activeCall.Wait, when no in-flight writeFrameInternal
// call can still receive a result from these requests.
func (s *Session) discardPendingWrites() {
	for {
		select {
		case req := <-s.shaper:
			resultChanPool.Put(req.result)
		default:
			goto drain_queue
		}
	}

drain_queue:
	for {
		req, ok := s.sq.Pop()
		if !ok {
			return
		}

		resultChanPool.Put(req.result)
	}
}

// notifyShaperPending wakes sendLoop when requests are ready to be written.
func (s *Session) notifyShaperPending() {
	select {
	case s.shaperPending <- struct{}{}:
	default:
	}
}

// notifyShaperConsumed allows shaperLoop to resume admitting requests.
func (s *Session) notifyShaperConsumed() {
	select {
	case s.shaperConsumed <- struct{}{}:
	default:
	}
}

// sendLoop serializes queued frames onto the underlying connection.
func (s *Session) sendLoop() {
	var buf []byte
	var vec [][]byte // vector for writeBuffers

	bw, ok := s.conn.(interface {
		WriteBuffers(v [][]byte) (n int, err error)
	})

	if ok {
		buf = make([]byte, headerSize)
		vec = make([][]byte, 2)
	} else {
		buf = make([]byte, (1<<16)+headerSize)
	}

	var n int
	var err error
EVENT_LOOP:
	for {
		select {
		case <-s.done:
			err = io.ErrClosedPipe
			break EVENT_LOOP
		case <-s.shaperPending:
			for {
				request, ok := s.sq.Pop()
				if !ok {
					// Let shaperLoop resume admissions now that the queue is empty.
					s.notifyShaperConsumed()
					goto EVENT_LOOP
				}

				buf[0] = request.frame.ver
				buf[1] = request.frame.cmd
				binary.LittleEndian.PutUint16(buf[2:], uint16(len(request.frame.data)))
				binary.LittleEndian.PutUint32(buf[4:], request.frame.sid)

				// Write the header and payload separately when supported.
				if len(vec) > 0 {
					vec[0] = buf[:headerSize]
					vec[1] = request.frame.data
					n, err = bw.WriteBuffers(vec)
				} else {
					copy(buf[headerSize:], request.frame.data)
					n, err = s.conn.Write(buf[:headerSize+len(request.frame.data)])
				}
				if err == nil && n != headerSize+len(request.frame.data) {
					err = io.ErrShortWrite
				}

				n -= headerSize
				n = max(n, 0)

				request.result <- writeResult{n: n, err: err}

				// Stop the writer after recording an underlying connection error.
				if err != nil {
					break EVENT_LOOP
				}
			}
		}
	}

	s.writeErr = err
	s.writeClosed.Store(true)
	// Publish the terminal write error before waking request waiters.
	close(s.writeDone)
}

// writeControlFrame queues a control frame and waits up to openCloseTimeout
// for its write result.
func (s *Session) writeControlFrame(f Frame) (n int, err error) {
	timer := time.NewTimer(openCloseTimeout)
	defer timer.Stop()

	return writeFrameInternal(s, f, timer.C, CLSCTRL)
}

// sendWriteRequest queues a frame before deadline, session shutdown, or a
// connection write error.
func sendWriteRequest[T any](s *Session, req writeRequest, deadline <-chan T) error {
	select {
	case s.shaper <- req:
		return nil
	case <-s.done:
		return io.ErrClosedPipe
	case <-s.writeDone:
		if s.IsClosed() {
			return io.ErrClosedPipe
		}

		return s.writeErr
	case <-deadline:
		if s.IsClosed() {
			return io.ErrClosedPipe
		}

		if s.writeClosed.Load() {
			return s.writeErr
		}

		return ErrTimeout
	}
}

// writeFrameInternal queues a frame and waits for its write result or deadline.
func writeFrameInternal[T any](s *Session, f Frame, deadline <-chan T, class CLASSID) (n int, err error) {
	if s.IsClosed() {
		return 0, io.ErrClosedPipe
	}

	if s.writeClosed.Load() {
		return 0, s.writeErr
	}

	if !s.activeCall.Add(1) {
		if s.IsClosed() {
			return 0, io.ErrClosedPipe
		}

		return 0, s.writeErr
	}
	defer s.activeCall.Done()

	// Reuse a result channel to avoid allocating one for every frame.
	resultCh := resultChanPool.Get().(chan writeResult)

	err = sendWriteRequest(
		s,
		writeRequest{
			class:  class,
			frame:  f,
			seq:    s.requestID.Add(1),
			result: resultCh,
		},
		deadline,
	)
	if err != nil {
		goto recycle
	}

	select {
	case result := <-resultCh:
		n, err = result.n, result.err
		goto recycle
	case <-s.done:
	case <-s.writeDone:
	case <-deadline:
	}

	if s.IsClosed() {
		return 0, io.ErrClosedPipe
	}

	if s.writeClosed.Load() {
		return 0, s.writeErr
	}

	// sendLoop may still deliver this request's result; keep the channel
	// out of the pool to prevent another request from reusing it.
	return 0, ErrTimeout

recycle:
	resultChanPool.Put(resultCh)
	return
}
