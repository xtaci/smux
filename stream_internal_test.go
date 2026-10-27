package smux

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type shortWriteWriter struct {
	n   int
	err error
}

func (w shortWriteWriter) Write(b []byte) (int, error) {
	return w.n, w.err
}

func TestStreamWriteToShortWrite(t *testing.T) {
	writeErr := errors.New("destination write failed")
	for _, version := range []int{1, 2} {
		for _, tc := range []shortWriteWriter{{n: 0}, {n: 3}, {n: 3, err: writeErr}} {
			t.Run(fmt.Sprintf("v%d/n=%d/error=%v", version, tc.n, tc.err), func(t *testing.T) {
				s := newUnitTestStream()
				s.sess.config.Version = version
				// Bound the wait if WriteTo incorrectly continues after the short write.
				s.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				buf := defaultAllocator.Get(6)
				copy(*buf, "abcdef")
				s.pushBytes(buf)
				s.fin()

				n, gotErr := io.Copy(tc, s)
				wantErr := tc.err
				if wantErr == nil {
					wantErr = io.ErrShortWrite
				}
				if gotErr != wantErr {
					t.Fatalf("copy error = %v; want %v", gotErr, wantErr)
				}
				if n != int64(tc.n) {
					t.Fatalf("copied %d bytes; want %d", n, tc.n)
				}
			})
		}
	}
}

func TestStreamWriteToReturnsForcedClose(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			s := newUnitTestStream()
			s.sess.config.Version = version
			s.sessionClose()

			if _, err := s.WriteTo(io.Discard); err != io.ErrClosedPipe {
				t.Fatalf("WriteTo error = %v; want %v", err, io.ErrClosedPipe)
			}
		})

		t.Run(fmt.Sprintf("v%d-after-fin", version), func(t *testing.T) {
			s := newUnitTestStream()
			s.sess.config.Version = version
			s.fin()
			s.writeClosedOnce.Do(func() { close(s.chWriteClosed) })
			s.tryHalfCloseCleanup()
			if err := s.Close(); err != io.ErrClosedPipe {
				t.Fatalf("Close error = %v; want %v", err, io.ErrClosedPipe)
			}

			if _, err := s.WriteTo(io.Discard); err != io.ErrClosedPipe {
				t.Fatalf("WriteTo error after forced close = %v; want %v", err, io.ErrClosedPipe)
			}
		})
	}
}

func TestStreamReadReturnsForcedClose(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			s := newUnitTestStream()
			s.sess.config.Version = version
			s.sessionClose()

			if _, err := s.Read(make([]byte, 1)); err != io.ErrClosedPipe {
				t.Fatalf("Read error = %v; want %v", err, io.ErrClosedPipe)
			}
		})
	}
}

func TestSessionCloseRecyclesStreamBuffers(t *testing.T) {
	s := newUnitTestStream()
	conn, peer := net.Pipe()
	defer peer.Close()
	s.sess.conn = conn
	s.sess.die = make(chan struct{})
	s.sess.chAccepts = make(chan *stream, 1)
	atomic.StoreInt32(&s.sess.bucket, int32(s.sess.config.MaxReceiveBuffer))
	s.sess.chAccepts <- s

	buf := defaultAllocator.Get(6)
	copy(*buf, "abcdef")
	s.pushBytes(buf)
	atomic.AddInt32(&s.sess.bucket, -6)

	if err := s.sess.Close(); err != nil {
		t.Fatal("session close failed:", err)
	}

	s.bufferLock.Lock()
	buffered := s.bufferRing.len()
	s.bufferLock.Unlock()
	if buffered != 0 {
		t.Fatalf("session close left %d buffers", buffered)
	}
	if len(s.sess.streams) != 0 {
		t.Fatalf("session close left %d streams", len(s.sess.streams))
	}
	if len(s.sess.chAccepts) != 0 {
		t.Fatalf("session close left %d pending accepts", len(s.sess.chAccepts))
	}
	if got := atomic.LoadInt32(&s.sess.bucket); got != int32(s.sess.config.MaxReceiveBuffer) {
		t.Fatalf("receive bucket = %d; want %d", got, s.sess.config.MaxReceiveBuffer)
	}
}

func TestSessionCloseDrainsPendingWriteRequests(t *testing.T) {
	cfg := DefaultConfig()
	cfg.KeepAliveDisabled = true
	conn, peer := net.Pipe()
	defer peer.Close()

	sess := newSession(cfg, conn, true)
	for i := 0; i < 32; i++ {
		frame := newFrame(byte(cfg.Version), cmdPSH, 1)
		frame.data = make([]byte, cfg.MaxFrameSize)
		sess.shaper <- writeRequest{
			class:  CLSDATA,
			frame:  frame,
			result: make(chan writeResult, 1),
		}
	}

	if err := sess.Close(); err != nil {
		t.Fatal("session close failed:", err)
	}
	if got := len(sess.shaper); got != 0 {
		t.Fatalf("session close left %d requests in shaper channel", got)
	}
	if got := sess.sq.Len(); got != 0 {
		t.Fatalf("session close left %d requests in shaper queue", got)
	}
}

type delayedWriteErrorConn struct {
	net.Conn
	started chan struct{}
	release <-chan struct{}
	err     error
}

func (c *delayedWriteErrorConn) Write([]byte) (int, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-c.release
	return 0, c.err
}

func TestSocketWriteErrorDrainsPendingWriteRequests(t *testing.T) {
	cfg := DefaultConfig()
	cfg.KeepAliveDisabled = true
	conn, peer := net.Pipe()
	defer peer.Close()

	writeErr := errors.New("write failed")
	release := make(chan struct{})
	wrapped := &delayedWriteErrorConn{
		Conn:    conn,
		started: make(chan struct{}, 1),
		release: release,
		err:     writeErr,
	}
	sess := newSession(cfg, wrapped, true)
	sess.shaper <- writeRequest{
		frame:  newFrame(byte(cfg.Version), cmdPSH, 1),
		result: make(chan writeResult, 1),
	}
	select {
	case <-wrapped.started:
	case <-time.After(time.Second):
		t.Fatal("sendLoop did not start writing")
	}

	for i := 0; i < 32; i++ {
		frame := newFrame(byte(cfg.Version), cmdPSH, 1)
		frame.data = make([]byte, cfg.MaxFrameSize)
		sess.shaper <- writeRequest{
			class:  CLSDATA,
			frame:  frame,
			result: make(chan writeResult, 1),
		}
	}
	close(release)

	select {
	case <-sess.chShaperDone:
	case <-time.After(time.Second):
		t.Fatal("shaperLoop did not stop after the socket write error")
	}
	if got := len(sess.shaper); got != 0 {
		t.Fatalf("socket write error left %d requests in shaper channel", got)
	}
	if got := sess.sq.Len(); got != 0 {
		t.Fatalf("socket write error left %d requests in shaper queue", got)
	}
	if err := sess.Close(); err != nil {
		t.Fatal("session close failed:", err)
	}
}

func TestNotifyWriteErrorDoesNotWaitForWriteAdmissionLock(t *testing.T) {
	sess := &Session{chSocketWriteError: make(chan struct{})}
	sess.writeReqLock.Lock()
	done := make(chan struct{})
	writeErr := errors.New("write failed")
	go func() {
		sess.notifyWriteError(writeErr)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		sess.writeReqLock.Unlock()
		t.Fatal("write error notification blocked on write admission lock")
	}
	sess.writeReqLock.Unlock()

	select {
	case <-sess.chSocketWriteError:
	default:
		t.Fatal("write error notification did not close the error channel")
	}
	if got := sess.socketWriteError.Load(); got != writeErr {
		t.Fatalf("stored write error = %v; want %v", got, writeErr)
	}
}

func TestDiscardPendingWritesSignalsWaitingWriters(t *testing.T) {
	sess := &Session{
		shaper: make(chan writeRequest, 2),
		sq:     NewShaperQueue(),
	}

	reqInChannel := writeRequest{
		frame:  newFrame(2, cmdPSH, 1),
		result: make(chan writeResult, 1),
	}
	reqInQueue := writeRequest{
		frame:  newFrame(2, cmdPSH, 2),
		result: make(chan writeResult, 1),
	}
	sess.shaper <- reqInChannel
	sess.sq.Push(reqInQueue)

	wantErr := errors.New("synthetic write error")
	sess.discardPendingWrites(wantErr)

	select {
	case res := <-reqInChannel.result:
		if res.err != wantErr {
			t.Fatalf("channel request error = %v; want %v", res.err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("channel request did not receive completion signal")
	}

	select {
	case res := <-reqInQueue.result:
		if res.err != wantErr {
			t.Fatalf("queue request error = %v; want %v", res.err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("queue request did not receive completion signal")
	}
}

func TestWriteFrameInternalDoesNotTimeoutWhenPendingWriteDiscarded(t *testing.T) {
	cfg := DefaultConfig()
	sess := &Session{
		config:             cfg,
		shaper:             make(chan writeRequest, 1),
		sq:                 NewShaperQueue(),
		die:                make(chan struct{}),
		chSocketReadError:  make(chan struct{}),
		chSocketWriteError: make(chan struct{}),
		chProtoError:       make(chan struct{}),
	}

	deadline := time.NewTimer(5 * time.Second)
	defer stopTimer(deadline)

	done := make(chan error, 1)
	go func() {
		_, err := sess.writeFrameInternal(newFrame(byte(cfg.Version), cmdPSH, 1), deadline.C, CLSDATA)
		done <- err
	}()

	waitQueued := time.NewTimer(time.Second)
	defer stopTimer(waitQueued)
	for len(sess.shaper) == 0 {
		select {
		case <-waitQueued.C:
			t.Fatal("write request was not queued in time")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	wantErr := errors.New("drained while closing")
	sess.discardPendingWrites(wantErr)

	select {
	case err := <-done:
		if err != wantErr {
			t.Fatalf("writeFrameInternal error = %v; want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("writeFrameInternal blocked after pending write was discarded")
	}
}

func TestRecvLoopDoesNotAddStreamAfterSessionClose(t *testing.T) {
	cfg := DefaultConfig()
	conn, peer := net.Pipe()
	defer peer.Close()

	sess := &Session{
		conn:               conn,
		config:             cfg,
		streams:            make(map[uint32]*stream),
		bucket:             int32(cfg.MaxReceiveBuffer),
		bucketNotify:       make(chan struct{}, 1),
		die:                make(chan struct{}),
		chAccepts:          make(chan *stream, 1),
		chSocketReadError:  make(chan struct{}),
		chSocketWriteError: make(chan struct{}),
		chProtoError:       make(chan struct{}),
	}
	recvDone := make(chan struct{})
	go func() {
		sess.recvLoop()
		close(recvDone)
	}()

	sess.streamLock.Lock()
	var hdr rawHeader
	hdr[0] = byte(cfg.Version)
	hdr[1] = cmdSYN
	binary.LittleEndian.PutUint32(hdr[4:], 123)
	writeDone := make(chan error, 1)
	go func() {
		_, err := peer.Write(hdr[:])
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			sess.streamLock.Unlock()
			t.Fatal("write SYN failed:", err)
		}
	case <-time.After(time.Second):
		sess.streamLock.Unlock()
		t.Fatal("recvLoop did not read SYN header")
	}

	atomic.StoreInt32(&sess.closed, 1)
	close(sess.die)
	sess.streamLock.Unlock()
	peer.Close()

	select {
	case <-recvDone:
	case <-time.After(time.Second):
		t.Fatal("recvLoop did not exit after peer close")
	}

	sess.streamLock.Lock()
	defer sess.streamLock.Unlock()
	if len(sess.streams) != 0 {
		t.Fatalf("closed session retained %d streams", len(sess.streams))
	}
	if len(sess.chAccepts) != 0 {
		t.Fatalf("closed session retained %d pending accepts", len(sess.chAccepts))
	}
}

func newUnitTestStream() *stream {
	cfg := DefaultConfig()
	sess := &Session{
		config:             cfg,
		streams:            make(map[uint32]*stream),
		chSocketReadError:  make(chan struct{}),
		chSocketWriteError: make(chan struct{}),
		chProtoError:       make(chan struct{}),
		bucketNotify:       make(chan struct{}, 1),
	}
	st := newStream(1, cfg.MaxFrameSize, sess)
	sess.streams[st.id] = st
	return st
}

func TestStreamWaitReadTimeout(t *testing.T) {
	s := newUnitTestStream()
	s.readDeadline.Store(time.Now().Add(20 * time.Millisecond))
	if err := s.waitRead(); err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestStreamWaitReadFinWithBufferedData(t *testing.T) {
	s := newUnitTestStream()
	buf := []byte("abc")
	s.pushBytes(&buf)

	s.fin()
	if err := s.waitRead(); err != nil {
		t.Fatalf("expected nil after fin with buffered data, got %v", err)
	}

	readBuf := make([]byte, 3)
	n, err := s.tryReadV1(readBuf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 bytes, got %d", n)
	}
	if !bytes.Equal(readBuf, []byte("abc")) {
		t.Fatalf("read mismatch: %q", readBuf)
	}
}

func TestStreamWaitReadHalfCloseWithBufferedData(t *testing.T) {
	s := newUnitTestStream()
	buf := []byte("abc")
	s.pushBytes(&buf)

	s.fin()
	s.writeClosedOnce.Do(func() { close(s.chWriteClosed) })
	s.tryHalfCloseCleanup()

	if err := s.waitRead(); err != nil {
		t.Fatalf("expected nil for buffered half-close stream, got %v", err)
	}
}

func TestStreamWaitReadHalfCloseDrainedEOF(t *testing.T) {
	s := newUnitTestStream()
	s.fin()
	s.writeClosedOnce.Do(func() { close(s.chWriteClosed) })
	s.tryHalfCloseCleanup()

	if err := s.waitRead(); err != io.EOF {
		t.Fatalf("expected io.EOF for drained half-close stream, got %v", err)
	}
}

func TestStreamRecycleTokens(t *testing.T) {
	s := newUnitTestStream()
	b1 := []byte("hello")
	b2 := []byte("world!")
	s.pushBytes(&b1)
	s.pushBytes(&b2)

	n := s.recycleTokens()
	if n != len(b1)+len(b2) {
		t.Fatalf("unexpected recycled bytes: %d", n)
	}
	if s.bufferRing.len() != 0 {
		t.Fatalf("expected empty buffer ring, got %d", s.bufferRing.len())
	}
}

func TestStreamRecycleTokensShrinksBufferRing(t *testing.T) {
	s := newUnitTestStream()
	for i := 0; i < defaultBufferRingCapacity*2; i++ {
		buf := defaultAllocator.Get(1)
		(*buf)[0] = byte(i)
		s.pushBytes(buf)
	}
	if len(s.bufferRing.bufs) <= defaultBufferRingCapacity {
		t.Fatal("expected buffer ring to grow")
	}

	if n := s.recycleTokens(); n != defaultBufferRingCapacity*2 {
		t.Fatalf("recycled %d bytes; want %d", n, defaultBufferRingCapacity*2)
	}
	if cap := len(s.bufferRing.bufs); cap != defaultBufferRingCapacity {
		t.Fatalf("buffer ring capacity = %d; want %d", cap, defaultBufferRingCapacity)
	}
}

func TestStreamUpdateNotifiesWriter(t *testing.T) {
	s := newUnitTestStream()
	s.update(7, 9)

	if got := atomic.LoadUint32(&s.peerConsumed); got != 7 {
		t.Fatalf("peerConsumed mismatch: %d", got)
	}
	if got := atomic.LoadUint32(&s.peerWindow); got != 9 {
		t.Fatalf("peerWindow mismatch: %d", got)
	}

	select {
	case <-s.chUpdate:
		// ok
	default:
		t.Fatal("expected chUpdate notification")
	}
}

func TestStreamSetDeadlineWakesUp(t *testing.T) {
	s := newUnitTestStream()
	if err := s.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline failed: %v", err)
	}

	select {
	case <-s.chReaderWakeup:
		// ok
	default:
		t.Fatal("expected reader wakeup")
	}

	select {
	case <-s.chWriterWakeup:
		// ok
	default:
		t.Fatal("expected writer wakeup")
	}
}

func TestStreamWaitReadClosed(t *testing.T) {
	s := newUnitTestStream()
	close(s.die)
	if err := s.waitRead(); err != io.ErrClosedPipe {
		t.Fatalf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestSendWindowUpdateTimeout(t *testing.T) {
	cfg := DefaultConfig()
	sess := &Session{
		config:             cfg,
		streams:            make(map[uint32]*stream),
		chSocketReadError:  make(chan struct{}),
		chSocketWriteError: make(chan struct{}),
		chProtoError:       make(chan struct{}),
		bucketNotify:       make(chan struct{}, 1),
		shaper:             nil,
		die:                make(chan struct{}),
	}
	st := newStream(1, cfg.MaxFrameSize, sess)
	st.readDeadline.Store(time.Now().Add(-time.Second))

	if err := st.sendWindowUpdate(1); err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestStopTimer(t *testing.T) {
	stopTimer(nil)

	timer := time.NewTimer(time.Nanosecond)
	<-timer.C
	stopTimer(timer)

	active := time.NewTimer(time.Second)
	stopTimer(active)
}

func TestNewBufferRingMinCapacity(t *testing.T) {
	r := newBufferRing(0)
	if len(r.bufs) != 1 {
		t.Fatalf("expected capacity 1, got %d", len(r.bufs))
	}
}

func newUnitTestStreamV2() *stream {
	cfg := DefaultConfig()
	cfg.Version = 2
	sess := &Session{
		config:             cfg,
		streams:            make(map[uint32]*stream),
		chSocketReadError:  make(chan struct{}),
		chSocketWriteError: make(chan struct{}),
		chProtoError:       make(chan struct{}),
		bucketNotify:       make(chan struct{}, 1),
		die:                make(chan struct{}),
	}
	st := newStream(1, cfg.MaxFrameSize, sess)
	sess.streams[st.id] = st
	return st
}

func TestWriteV2ClosedPipe(t *testing.T) {
	s := newUnitTestStreamV2()
	close(s.chWriteClosed)

	if _, err := s.writeV2([]byte("x")); err != io.ErrClosedPipe {
		t.Fatalf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestWriteV2ConsumedError(t *testing.T) {
	s := newUnitTestStreamV2()
	atomic.StoreUint32(&s.peerConsumed, 10)
	atomic.StoreUint32(&s.numWritten, 0)
	atomic.StoreUint32(&s.peerWindow, 0)

	if _, err := s.writeV2([]byte("x")); err != ErrConsumed {
		t.Fatalf("expected ErrConsumed, got %v", err)
	}
}

func TestWriteV2TimeoutWhenWindowZero(t *testing.T) {
	s := newUnitTestStreamV2()
	atomic.StoreUint32(&s.peerWindow, 0)
	s.writeDeadline.Store(time.Now().Add(-time.Second))

	if _, err := s.writeV2([]byte("data")); err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}
