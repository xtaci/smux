package smux

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

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
