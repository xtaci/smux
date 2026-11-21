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
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/transport/v5/deadline"
)

// Stream is a wrapper around stream that exposes the public API.
type Stream struct {
	*stream
	finalizer runtime.Cleanup
}

// Stream implements net.Conn
type stream struct {
	id   uint32 // Stream identifier
	sess *Session

	bufferRing bufferRing // ring buffer for ordered incoming data

	bufferLock sync.Mutex // Mutex to protect access to buffers
	frameSize  int        // Maximum frame size for the stream

	// wakeup channels
	chReaderWakeup chan struct{}
	chWriterWakeup chan struct{}

	// stream closing
	done        chan struct{}
	isClosed    atomic.Bool // Tracks full closure and guards closing done once.
	forcedClose atomic.Bool // Set when Close or Session.Close aborts the stream.

	v2WriteLock sync.Mutex // Serializes v2 window reservations between writers.

	// Signals that the peer has closed its write side by sending FIN.
	readDone     chan struct{}
	isReadClosed atomic.Bool // Ensures readDone is closed only once

	// Local write-side closure; the FIN may still be in flight.
	writeDone     chan struct{}
	isWriteClosed atomic.Bool // Ensures writeDone is closed only once

	// read/write deadline
	readDeadline, writeDeadline *deadline.Deadline

	// v2 stream fields(flow control)
	numRead    uint32        // cumulative bytes consumed from the stream
	numWritten atomic.Uint32 // count num of bytes written
	incr       uint32        // bytes consumed since the last window update

	// UPD command
	peerConsumed          atomic.Uint32 // num of bytes the peer has consumed
	peerWindow            uint32        // peer window, initialized to 256 KiB and updated by peer
	chUpdate              chan struct{} // notify of remote data consuming and window update
	windowUpdateThreshold uint32        // cached threshold for window update (MaxStreamBuffer/2)
}

type bufferRing struct {
	bufs  [][]byte
	heads []*[]byte
	head  int
	tail  int
	size  int
	mask  int // bitmask for fast modulo when capacity is power of 2
}

const defaultBufferRingCapacity = 8

// newBufferRing creates an empty ring with power-of-two capacity.
func newBufferRing(capacity int) bufferRing {
	if capacity < 1 {
		capacity = 1
	}
	// Round up to a power of two so ring indices can use a bitmask.
	cap := 1
	for cap < capacity {
		cap <<= 1
	}
	return bufferRing{
		bufs:  make([][]byte, cap),
		heads: make([]*[]byte, cap),
		mask:  cap - 1,
	}
}

// len returns the number of buffers currently stored in the ring.
func (r *bufferRing) len() int {
	return r.size
}

// grow doubles the ring capacity while preserving buffer order.
func (r *bufferRing) grow() {
	newCap := max(len(r.bufs)*2, 1)
	newBufs := make([][]byte, newCap)
	newHeads := make([]*[]byte, newCap)
	for i := 0; i < r.size; i++ {
		idx := (r.head + i) & r.mask
		newBufs[i] = r.bufs[idx]
		newHeads[i] = r.heads[idx]
	}
	r.bufs = newBufs
	r.heads = newHeads
	r.head = 0
	r.tail = r.size
	r.mask = newCap - 1
}

// push appends a buffer and its allocator handle to the ring.
func (r *bufferRing) push(buf []byte, head *[]byte) {
	if r.size == len(r.bufs) {
		r.grow()
	}
	r.bufs[r.tail] = buf
	r.heads[r.tail] = head
	r.tail = (r.tail + 1) & r.mask
	r.size++
}

// pop removes and returns the oldest buffer and allocator handle.
func (r *bufferRing) pop() (buf []byte, head *[]byte, ok bool) {
	if r.size == 0 {
		return nil, nil, false
	}
	buf = r.bufs[r.head]
	head = r.heads[r.head]
	r.bufs[r.head] = nil
	r.heads[r.head] = nil
	r.head = (r.head + 1) & r.mask
	r.size--
	if r.size == 0 {
		r.tail = r.head
		r.shrinkIfEmpty()
	}
	return buf, head, true
}

// shrinkIfEmpty restores the default capacity after a large ring is drained.
func (r *bufferRing) shrinkIfEmpty() {
	if r.size == 0 && len(r.bufs) > defaultBufferRingCapacity {
		*r = newBufferRing(defaultBufferRingCapacity)
	}
}

// consumeFront copies data from the front buffer into b and recycles the buffer
// when fully consumed. It returns the number of bytes copied and the recycled
// buffer, if any.
func (r *bufferRing) consumeFront(b []byte) (n int, recycled *[]byte) {
	if r.size == 0 {
		return 0, nil
	}
	n = copy(b, r.bufs[r.head])
	r.bufs[r.head] = r.bufs[r.head][n:]

	// Release the allocator buffer once all of its data has been consumed.
	if len(r.bufs[r.head]) == 0 {
		recycled = r.heads[r.head]
		r.bufs[r.head] = nil
		r.heads[r.head] = nil
		r.head = (r.head + 1) & r.mask
		r.size--
		if r.size == 0 {
			r.tail = r.head
			r.shrinkIfEmpty()
		}
	}
	return n, recycled
}

// newStream initializes the internal state for a stream.
func newStream(id uint32, frameSize int, sess *Session) *stream {
	s := new(stream)
	s.id = id
	s.chReaderWakeup = make(chan struct{}, 1)
	s.chWriterWakeup = make(chan struct{}, 1)
	s.chUpdate = make(chan struct{}, 1)
	s.frameSize = frameSize
	s.sess = sess
	s.done = make(chan struct{})
	s.readDone = make(chan struct{})
	s.writeDone = make(chan struct{})                                 // Signals local write-side closure.
	s.peerWindow = initialPeerWindow                                  // Initial peer receive window.
	s.windowUpdateThreshold = uint32(sess.config.MaxStreamBuffer / 2) // Cached update threshold.
	// Preallocate the receive ring to reduce allocations during data transfer.
	s.bufferRing = newBufferRing(defaultBufferRingCapacity)

	s.readDeadline = deadline.New()
	s.writeDeadline = deadline.New()

	// Clear pending timers when the stream is collected instead of retaining
	// deadline resources until they expire.
	runtime.AddCleanup(s, freeDeadline, s.readDeadline)
	runtime.AddCleanup(s, freeDeadline, s.writeDeadline)

	return s
}

func freeStream(s *stream) {
	s.Close()
}

// ID returns the stream's unique identifier.
func (s *Stream) ID() uint32 {
	return s.id
}

// Read reads data from the stream into b.
func (s *Stream) Read(b []byte) (n int, err error) {
	// keep the wrapper reachable until the read completes.
	defer runtime.KeepAlive(s)

	if s.sess.config.Version == 2 {
		for {
			n, err = s.tryReadV2(b)
			if err != ErrWouldBlock {
				return n, err
			}

			err = s.waitRead()
			if err != nil {
				return 0, err
			}
		}
	}

	for {
		n, err = s.tryReadV1(b)
		if err != ErrWouldBlock {
			return n, err
		}

		err = s.waitRead()
		if err != nil {
			return 0, err
		}
	}
}

// tryReadV1 reads buffered data without blocking, using protocol version 1
// behavior.
func (s *stream) tryReadV1(b []byte) (n int, err error) {
	if s.isForcedClosed() {
		return 0, io.ErrClosedPipe
	}

	err = s.readDeadline.Err()
	if err != nil {
		return 0, ErrTimeout
	}

	if len(b) == 0 {
		return 0, nil
	}

	// Protect the ring while copying the next available data into b.
	var recycled *[]byte
	s.bufferLock.Lock()
	n, recycled = s.bufferRing.consumeFront(b)
	s.bufferLock.Unlock()

	if recycled != nil {
		free(recycled)
	}

	// Return shared receive capacity so recvLoop can accept more data.
	if n > 0 {
		s.sess.returnTokens(n)
		// Both FINs may have arrived while data remained buffered. Retry cleanup
		// now that these bytes have been delivered.
		s.tryHalfCloseCleanup()
		return n, nil
	}

	// Deliver buffered data before reporting EOF or a forced-close error.
	select {
	case <-s.done:
		if s.isForcedClosed() {
			return 0, io.ErrClosedPipe
		}
		return 0, io.EOF
	case <-s.readDeadline.Done():
		return 0, ErrTimeout
	default:
		return 0, ErrWouldBlock
	}
}

// tryReadV2 is the non-blocking version of Read for version 2 streams.
func (s *stream) tryReadV2(b []byte) (n int, err error) {
	if s.isForcedClosed() {
		return 0, io.ErrClosedPipe
	}

	err = s.readDeadline.Err()
	if err != nil {
		return 0, ErrTimeout
	}

	if len(b) == 0 {
		return 0, nil
	}

	var notifyConsumed uint32
	var recycled *[]byte
	s.bufferLock.Lock()
	n, recycled = s.bufferRing.consumeFront(b)

	// Track consumed bytes and send periodic window updates. Updating after
	// roughly half the window is consumed helps prevent a continuous reader
	// from stalling while waiting for acknowledgements.
	//
	// s.numRead == n identifies the first read from this stream.
	s.numRead += uint32(n)
	s.incr += uint32(n)

	// Send an update once the threshold is reached, and always on the first read.
	if s.incr >= s.windowUpdateThreshold || s.numRead == uint32(n) {
		notifyConsumed = s.numRead
		s.incr = 0 // Start counting toward the next update.
	}
	s.bufferLock.Unlock()

	if recycled != nil {
		free(recycled)
	}

	if n > 0 {
		s.sess.returnTokens(n)

		// Report consumption when the update threshold has been reached.
		if notifyConsumed > 0 {
			err := s.sendWindowUpdate(notifyConsumed)
			// The bytes are already consumed, so cleanup must proceed even if
			// sending the update fails.
			s.tryHalfCloseCleanup()
			return n, err
		}
		s.tryHalfCloseCleanup()
		return n, nil
	}

	select {
	case <-s.done:
		if s.isForcedClosed() {
			return 0, io.ErrClosedPipe
		}
		return 0, io.EOF
	default:
		return 0, ErrWouldBlock
	}
}

// WriteTo implements io.WriteTo
// WriteTo writes stream data to w until the stream reaches EOF or an error occurs.
// It returns the number of bytes written and any error encountered. For protocol
// version 2, it sends window updates to the peer as needed.
func (s *Stream) WriteTo(w io.Writer) (n int64, err error) {
	// Keep the wrapper reachable until the write completes.
	defer runtime.KeepAlive(s)

	switch s.sess.config.Version {
	case 2:
		return s.writeToV2(w)
	default:
		return s.writeToV1(w)
	}
}

// writeToV1 drains buffered data to w without protocol version 2 updates.
func (s *stream) writeToV1(w io.Writer) (n int64, err error) {
	for {
		if s.isForcedClosed() {
			return n, io.ErrClosedPipe
		}

		err = s.readDeadline.Err()
		if err != nil {
			return n, ErrTimeout
		}

		var buf []byte
		var head *[]byte

		// Remove the next queued buffer while holding the ring lock.
		s.bufferLock.Lock()
		if s.bufferRing.len() > 0 {
			buf, head, _ = s.bufferRing.pop()
		}
		s.bufferLock.Unlock()

		// Write the dequeued buffer to the destination.
		if buf != nil {
			nw, ew := w.Write(buf)
			// WriteTo consumes receive data just like Read, so return its tokens.
			s.sess.returnTokens(len(buf))
			free(head)
			if nw > 0 {
				n += int64(nw)
			}

			if ew != nil {
				s.tryHalfCloseCleanup()
				return n, ew
			}
			if nw != len(buf) {
				s.tryHalfCloseCleanup()
				return n, io.ErrShortWrite
			}
			s.tryHalfCloseCleanup()
			continue
		}

		err = s.waitRead()
		switch err {
		case nil:
		case io.EOF:
			return n, nil

		default:
			return n, err
		}
	}
}

// writeToV2 drains buffered data to w and reports consumed bytes to the peer.
func (s *stream) writeToV2(w io.Writer) (n int64, err error) {
	for {
		if s.isForcedClosed() {
			return n, io.ErrClosedPipe
		}

		err = s.readDeadline.Err()
		if err != nil {
			return n, ErrTimeout
		}

		var notifyConsumed uint32
		var buf []byte
		var head *[]byte

		// Remove the next queued buffer while holding the ring lock.
		s.bufferLock.Lock()
		if s.bufferRing.len() > 0 {
			buf, head, _ = s.bufferRing.pop()
		}

		// Account for bytes consumed by WriteTo for version 2 flow control.
		var bufLen uint32
		if buf != nil {
			bufLen = uint32(len(buf))
		}
		s.numRead += bufLen
		s.incr += bufLen

		// Send an update after the threshold is reached or on the first read.
		if s.incr >= s.windowUpdateThreshold || s.numRead == bufLen {
			notifyConsumed = s.numRead
			s.incr = 0
		}
		s.bufferLock.Unlock()

		// Write the dequeued buffer to the destination.
		if buf != nil {
			nw, ew := w.Write(buf)
			// WriteTo consumes receive data just like Read, so return its tokens.
			s.sess.returnTokens(len(buf))
			free(head)
			if nw > 0 {
				n += int64(nw)
			}

			if ew != nil {
				if notifyConsumed > 0 {
					_ = s.sendWindowUpdate(notifyConsumed)
				}
				s.tryHalfCloseCleanup()
				return n, ew
			}
			if nw != len(buf) {
				if notifyConsumed > 0 {
					_ = s.sendWindowUpdate(notifyConsumed)
				}
				s.tryHalfCloseCleanup()
				return n, io.ErrShortWrite
			}

			// Report consumed bytes so the peer can advance its send window.
			if notifyConsumed > 0 {
				if err := s.sendWindowUpdate(notifyConsumed); err != nil {
					s.tryHalfCloseCleanup()
					return n, err
				}
			}
			s.tryHalfCloseCleanup()
			continue
		}

		err = s.waitRead()
		switch err {
		case nil:
		case io.EOF:
			return n, nil

		default:
			return n, err
		}
	}
}

// sendWindowUpdate reports the number of bytes consumed and the receive window to the peer.
func (s *stream) sendWindowUpdate(consumed uint32) error {
	frame := newFrame(byte(s.sess.config.Version), cmdUPD, s.id)
	var hdr updHeader
	binary.LittleEndian.PutUint32(hdr[:], consumed)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(s.sess.config.MaxStreamBuffer))
	frame.data = hdr[:]
	_, err := writeFrameInternal(s.sess, frame, s.readDeadline.Done(), CLSCTRL) // Keep flow-control updates ahead of data frames.
	return err
}

// waitRead blocks until a read condition changes, the stream or session read
// side closes, a session error occurs, or the read deadline expires.
//
// Note that tryRead* is non-blocking and only falls back to waitRead when the
// receive ring is empty, so this function is entered after the ring was found
// empty.
//
// The state-channel cases (readDone, sess.readDone, done) can become ready
// concurrently. selectgo picks one case at random. The readDone and
// sess.readDone branches check the relevant stream state before returning;
// the done case converges on the isForcedClosed / buffer-drained tail below.
// This resolves the observable read state as follows:
//
//   - a forced close is checked before the common tail can report a clean EOF;
//   - buffered data remains readable after the peer half-closes if no deadline
//     has expired: the receive ring is checked before returning EOF;
//   - io.EOF is reported only once the ring is actually drained.
//
// A wakeup from chReaderWakeup can return directly because the next tryRead*
// re-checks both forced close and the deadline before consuming data. The
// read-deadline branch checks for forced close and a published session read
// error before returning ErrTimeout. A session read error can return directly
// when the stream is still open and has not received peer FIN.
func (s *stream) waitRead() error {
	select {
	case <-s.chReaderWakeup:
		// A read condition may have changed.
		// The next round read will recheck force-close and ReadDeadline.
		return nil
	case <-s.readDone:
		if s.isClosed.Load() {
			// The stream is fully closed; resolve forced-close or normal EOF below.
			break
		}

		// Check again for forced close to resolve a race in Close.
		if s.isForcedClosed() {
			return io.ErrClosedPipe
		}

		// If the stream is half-closed, but the read deadline has expired, return ErrTimeout.
		if s.readDeadline.Err() != nil {
			return ErrTimeout
		}

		// Peer closed the write side, but there may still be buffered data to read.
		// Check the buffer before returning EOF so queued data remains readable.
		s.bufferLock.Lock()
		defer s.bufferLock.Unlock()
		if s.bufferRing.len() > 0 {
			return nil
		}
		if s.isForcedClosed() {
			return io.ErrClosedPipe
		}
		return io.EOF
	case <-s.sess.readDone:
		// The session read error may have raced with full stream closure.
		if s.isClosed.Load() {
			break
		}

		// Check again for forced close to resolve a race in Close.
		if s.isForcedClosed() {
			return io.ErrClosedPipe
		}

		if s.isReadClosed.Load() {
			// Peer closed the write side, but there may still be buffered data to read.
			// Check the buffer before returning EOF so queued data remains readable.

			// If the stream is half-closed, but the read deadline has expired, return ErrTimeout.
			if s.readDeadline.Err() != nil {
				return ErrTimeout
			}

			s.bufferLock.Lock()
			defer s.bufferLock.Unlock()

			if s.bufferRing.len() > 0 {
				return nil
			}

			if s.isForcedClosed() {
				return io.ErrClosedPipe
			}
			return io.EOF
		}

		// The session read side is closed; return its error.
		return s.sess.readErr
	case <-s.readDeadline.Done():
		if s.isForcedClosed() {
			return io.ErrClosedPipe
		}

		if s.sess.readClosed.Load() {
			return s.sess.readErr
		}

		return ErrTimeout
	case <-s.done:
		// The stream is fully closed; resolve its final read state below.
	}

	// A forced close takes precedence over buffered data.
	if s.isForcedClosed() {
		return io.ErrClosedPipe
	}

	// A normal full close does not override an expired read deadline.
	if s.readDeadline.Err() != nil {
		return ErrTimeout
	}

	// Deliver any remaining data before reporting EOF.
	s.bufferLock.Lock()
	defer s.bufferLock.Unlock()

	if s.bufferRing.len() > 0 {
		return nil
	}
	return io.EOF
}

// checkWriteClosed reports whether the local write side or whole stream is
// closed, or whether the write deadline has already expired.
//
// When writeDeadline.Done() fires, it may race with stream closure (done) or
// local half-close (writeDone). Because selectgo chooses randomly among
// simultaneously ready cases, the deadline case checks isClosed and
// isWriteClosed before returning ErrTimeout so a close racing with deadline
// expiry is never masked as a benign timeout.
func (s *stream) checkWriteClosed() error {
	select {
	case <-s.writeDone:
		// The local write side was closed.
		return io.ErrClosedPipe
	case <-s.done:
		// The stream was fully closed.
		return io.ErrClosedPipe
	case <-s.writeDeadline.Done():
		if s.isClosed.Load() {
			return io.ErrClosedPipe
		}

		if s.isWriteClosed.Load() {
			return io.ErrClosedPipe
		}

		return ErrTimeout
	default:
		return nil
	}
}

// Write implements net.Conn
//
// Concurrent Write calls may interleave at frame boundaries, and their ordering
// is nondeterministic.
func (s *Stream) Write(b []byte) (n int, err error) {
	// keep the wrapper reachable until the write completes.
	defer runtime.KeepAlive(s)

	switch s.sess.config.Version {
	case 2:
		return s.writeV2(b)
	default:
		return s.writeV1(b)
	}
}

// writeV1 splits and sends data frames without per-stream flow control.
func (s *stream) writeV1(b []byte) (n int, err error) {
	// An empty write has nothing to send.
	if len(b) == 0 {
		return 0, nil
	}

	// Reject writes after the local write side or stream has closed.
	if err := s.checkWriteClosed(); err != nil {
		return 0, err
	}

	// Split the input into frames and send them in order.
	sent := 0
	frame := newFrame(byte(s.sess.config.Version), cmdPSH, s.id)
	for len(b) > 0 {
		size := min(len(b), s.frameSize)

		frame.data = b[:size]
		s.numWritten.Add(uint32(size))
		n, err := writeFrameInternal(s.sess, frame, s.writeDeadline.Done(), CLSDATA)
		sent += n
		if err != nil {
			return sent, err
		}

		b = b[size:]
	}

	return sent, nil
}

// writeV2 sends data frames subject to the peer's advertised receive window.
func (s *stream) writeV2(b []byte) (n int, err error) {
	// An empty write has nothing to send.
	if len(b) == 0 {
		return 0, nil
	}

	// Reject writes after the local write side or stream has closed.
	if err := s.checkWriteClosed(); err != nil {
		return 0, err
	}
	s.v2WriteLock.Lock()
	defer s.v2WriteLock.Unlock()

	// Split the input into frames, respecting the peer's receive window.
	sent := 0
	frame := newFrame(byte(s.sess.config.Version), cmdPSH, s.id)

	for {
		// Calculate bytes in flight within this stream's sliding window.
		// [.... [consumed... numWritten] ... win... ]
		// [.... [consumed...................+rmtwnd]]
		// Unsigned subtraction handles counter wraparound modulo 2^32:
		// uint32(0) - uint32(math.MaxUint32) = 1
		// int32(uint32(0) - uint32(1)) = -1
		//
		// The signed result distinguishes a valid in-flight count from an
		// invalid peer-consumed count.
		inflight := int32(s.numWritten.Load() - s.peerConsumed.Load())
		if inflight < 0 { // Reject an inconsistent peer-consumed count.
			return 0, ErrConsumed
		}

		// Compute remaining window space using the same modulo-2^32 counters.
		win := int32(atomic.LoadUint32(&s.peerWindow)) - inflight

		if win > 0 {
			// Limit this batch to the available window space.
			n := min(len(b), int(win))

			// Split the batch into frames and send them in order.
			bts := b[:n]
			for len(bts) > 0 {
				// Keep each frame within the configured maximum size.
				size := min(len(bts), s.frameSize)
				frame.data = bts[:size]

				s.numWritten.Add(uint32(size))
				nw, err := writeFrameInternal(s.sess, frame, s.writeDeadline.Done(), CLSDATA)
				sent += nw
				if err != nil {
					return sent, err
				}

				bts = bts[size:]
			}

			b = b[n:]
		}

		// Return once all input bytes have been sent.
		if len(b) <= 0 {
			return sent, nil
		}

		// Wait for more window space, closure, a session write error, or the
		// write deadline. Blocking here propagates flow control to the caller
		// as backpressure.
		//
		// Cases that wake to re-evaluate window/state or converge on post-select
		// checks (chWriterWakeup, chUpdate, writeDeadline.Done(), sess.writeDone)
		// do not return per case, because multiple signals can be ready at once
		// and selectgo picks among them randomly. Evaluating isClosed,
		// isWriteClosed, writeDeadline.Err(), and sess.writeErr in a fixed
		// order after select makes concurrent close/timeout/error races
		// deterministic, while returning immediately on done or writeDone still
		// short-circuits those explicit close signals.
		select {
		case <-s.done:
			return sent, io.ErrClosedPipe
		case <-s.writeDone:
			// The local write side was half-closed.
			return sent, io.ErrClosedPipe
		case <-s.chWriterWakeup:
			// The stream's writable state may have changed.
			// Triggered by SetWriteDeadline
		case <-s.chUpdate:
			// The peer consumed data or updated its receive window.
		case <-s.writeDeadline.Done():
			// The write deadline exceeded.
		case <-s.sess.writeDone:
		}

		if s.isClosed.Load() {
			return sent, io.ErrClosedPipe
		}

		if s.isWriteClosed.Load() {
			return sent, io.ErrClosedPipe
		}

		if s.sess.writeClosed.Load() {
			return sent, s.sess.writeErr
		}

		if s.writeDeadline.Err() != nil {
			return sent, ErrTimeout
		}
	}
}

// CloseWrite implements half-close by closing the write side of the stream.
// After CloseWrite, the stream can still receive data from the peer,
// but any further writes will return io.ErrClosedPipe.
// This is similar to net.TCPConn.CloseWrite().
func (s *Stream) CloseWrite() error {
	if s.isWriteClosed.Swap(true) {
		return io.ErrClosedPipe
	}
	close(s.writeDone)

	// Notify the peer that no more data will be written on this stream.
	f := newFrame(byte(s.sess.config.Version), cmdFIN, s.id)

	timer := time.NewTimer(openCloseTimeout)
	defer timer.Stop()

	_, err := writeFrameInternal(s.sess, f, timer.C, CLSDATA)
	s.tryHalfCloseCleanup()
	return err
}

// Close fully closes the stream (both read and write sides).
func (s *stream) Close() error {
	s.forcedClose.Store(true)

	if s.isClosed.Swap(true) {
		// Half-close cleanup may have closed done while unread data kept the
		// stream registered. An explicit Close discards that data, so remove
		// the stream and reclaim its buffers now.
		s.sess.streamClosed(s.id)
		return io.ErrClosedPipe
	}

	close(s.done)

	// Mark the local write side closed if it has not already been closed.
	if !s.isWriteClosed.Swap(true) {
		close(s.writeDone)
	}

	// Send FIN after prior data frames so the peer observes them in order.
	f := newFrame(byte(s.sess.config.Version), cmdFIN, s.id)

	timer := time.NewTimer(openCloseTimeout)
	defer timer.Stop()

	_, err := writeFrameInternal(s.sess, f, timer.C, CLSDATA) // Order FIN with the stream's data frames.
	s.sess.streamClosed(s.id)
	return err
}

// Close implements net.Conn
func (s *Stream) Close() error {
	s.finalizer.Stop()
	// Keep the cleanup target reachable until Stop has taken effect.
	runtime.KeepAlive(s)
	return s.stream.Close()
}

// GetDieCh returns a channel that is closed when the stream is closed.
func (s *Stream) GetDieCh() <-chan struct{} {
	return s.done
}

// SetReadDeadline sets the read deadline as defined by
// net.Conn.SetReadDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readDeadline.Set(t)
	s.wakeupReader()
	return nil
}

// SetWriteDeadline sets the write deadline as defined by
// net.Conn.SetWriteDeadline.
// A zero time value disables the deadline.
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.Set(t)
	s.wakeupWriter()
	return nil
}

// SetDeadline sets both read and write deadlines as defined by
// net.Conn.SetDeadline.
// A zero time value disables the deadlines.
func (s *Stream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	if err := s.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// sessionClose closes the stream when its session shuts down.
func (s *stream) sessionClose() {
	s.forcedClose.Store(true)
	if s.isClosed.Swap(true) {
		return
	}
	close(s.done)
}

// LocalAddr returns the underlying connection's local address, if available.
func (s *Stream) LocalAddr() net.Addr {
	if ts, ok := s.sess.conn.(interface {
		LocalAddr() net.Addr
	}); ok {
		return ts.LocalAddr()
	}
	return nil
}

// RemoteAddr returns the underlying connection's remote address, if available.
func (s *Stream) RemoteAddr() net.Addr {
	if ts, ok := s.sess.conn.(interface {
		RemoteAddr() net.Addr
	}); ok {
		return ts.RemoteAddr()
	}
	return nil
}

// pushBytes appends buf to the stream's receive buffer.
func (s *stream) pushBytes(pbuf *[]byte) {
	s.bufferLock.Lock()
	defer s.bufferLock.Unlock()
	s.bufferRing.push(*pbuf, pbuf)
}

// recycleTokens returns the number of unread bytes and releases their buffers.
func (s *stream) recycleTokens() (n int) {
	s.bufferLock.Lock()
	defer s.bufferLock.Unlock()
	for s.bufferRing.len() > 0 {
		buf, head, _ := s.bufferRing.pop()
		n += len(buf)
		free(head)
	}
	return
}

// wakeupReader signals a blocked reader that stream state may have changed.
func (s *stream) wakeupReader() {
	select {
	case s.chReaderWakeup <- struct{}{}:
	default:
	}
}

// wakeupWriter signals a blocked writer that stream state may have changed.
func (s *stream) wakeupWriter() {
	select {
	case s.chWriterWakeup <- struct{}{}:
	default:
	}
}

// update records the peer's consumed-byte count and receive-window size.
func (s *stream) update(consumed uint32, window uint32) {
	// Publish both flow-control values before waking a blocked writer.
	s.peerConsumed.Store(consumed)
	atomic.StoreUint32(&s.peerWindow, window)

	// Wake a writer waiting for window space or a peer update.
	select {
	case s.chUpdate <- struct{}{}:
	default:
	}
}

// fin records that the peer has closed its write side by sending FIN.
func (s *stream) fin() {
	if !s.isReadClosed.Swap(true) {
		close(s.readDone)
	}
	s.tryHalfCloseCleanup()
}

// tryHalfCloseCleanup closes the stream after both directions are closed and
// removes it from the session once its buffered data has been consumed.
func (s *stream) tryHalfCloseCleanup() {
	if !s.isReadClosed.Load() {
		return
	}

	if !s.isWriteClosed.Load() {
		return
	}

	if !s.isClosed.Swap(true) {
		close(s.done)
	}

	// Closing done wakes readers so they can observe EOF, but must not discard
	// bytes that recvLoop queued before the peer's FIN. Keep the stream in the
	// session map until those bytes are consumed; streamClosed recycles any
	// remainder, so it is safe to call only after the ring is empty.
	s.bufferLock.Lock()
	drained := s.bufferRing.len() == 0
	s.bufferLock.Unlock()
	if drained {
		s.sess.streamClosed(s.id)
	}
}

// isForcedClosed reports whether the stream was closed explicitly or by its session.
func (s *stream) isForcedClosed() bool {
	return s.forcedClose.Load()
}
