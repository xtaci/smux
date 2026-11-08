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
	"container/heap"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestShaper(t *testing.T) {
	w1 := writeRequest{seq: 1}
	w2 := writeRequest{seq: 2}
	w3 := writeRequest{seq: 3}
	w4 := writeRequest{seq: 4}
	w5 := writeRequest{seq: 5}

	var reqs shaperHeap
	heap.Push(&reqs, w5)
	heap.Push(&reqs, w4)
	heap.Push(&reqs, w3)
	heap.Push(&reqs, w2)
	heap.Push(&reqs, w1)

	wantSeqs := []uint32{1, 2, 3, 4, 5}
	for _, want := range wantSeqs {
		if len(reqs) == 0 {
			t.Fatalf("heap empty before sequence %d", want)
		}
		w := heap.Pop(&reqs).(writeRequest)
		if w.seq != want {
			t.Fatalf("heap popped sequence %d; want %d", w.seq, want)
		}
	}
	if len(reqs) != 0 {
		t.Fatalf("heap contains %d requests after expected pops", len(reqs))
	}
}

func TestShaper2(t *testing.T) {
	w1 := writeRequest{class: CLSDATA, seq: 1} // stream 0
	w2 := writeRequest{class: CLSDATA, seq: 2}
	w3 := writeRequest{class: CLSDATA, seq: 3}
	w4 := writeRequest{class: CLSDATA, seq: 4}
	w5 := writeRequest{class: CLSDATA, seq: 5}
	w6 := writeRequest{class: CLSCTRL, seq: 6, frame: Frame{sid: 10}} // ctrl 1
	w7 := writeRequest{class: CLSCTRL, seq: 7, frame: Frame{sid: 11}} // ctrl 2

	var reqs shaperHeap
	heap.Push(&reqs, w6)
	heap.Push(&reqs, w5)
	heap.Push(&reqs, w4)
	heap.Push(&reqs, w3)
	heap.Push(&reqs, w2)
	heap.Push(&reqs, w1)
	heap.Push(&reqs, w7)

	wantSeqs := []uint32{6, 7, 1, 2, 3, 4, 5}
	for _, want := range wantSeqs {
		if len(reqs) == 0 {
			t.Fatalf("heap empty before sequence %d", want)
		}
		w := heap.Pop(&reqs).(writeRequest)
		if w.seq != want {
			t.Fatalf("heap popped sequence %d; want %d", w.seq, want)
		}
	}
	if len(reqs) != 0 {
		t.Fatalf("heap contains %d requests after expected pops", len(reqs))
	}
}

func TestShaperQueueFairness(t *testing.T) {
	sq := NewShaperQueue()
	const (
		streams           = 10
		requestsPerStream = 16
	)

	for sid := range streams {
		for seq := range requestsPerStream {
			sq.Push(writeRequest{
				frame: Frame{sid: uint32(sid)},
				seq:   uint32(seq),
			})
		}
	}

	for index := range streams * requestsPerStream {
		req, ok := sq.Pop()
		if !ok {
			t.Fatalf("queue became empty after %d of %d pops", index, streams*requestsPerStream)
		}

		wantSID := uint32(index % streams)
		wantSeq := uint32(index / streams)
		if req.frame.sid != wantSID || req.seq != wantSeq {
			t.Fatalf("pop %d = (sid=%d, seq=%d); want (sid=%d, seq=%d)",
				index, req.frame.sid, req.seq, wantSID, wantSeq)
		}
	}
	if !sq.IsEmpty() || sq.Len() != 0 {
		t.Fatalf("queue not empty after draining: len=%d", sq.Len())
	}
}

func TestShaperQueueConcurrentStress(t *testing.T) {
	if os.Getenv("SMUX_SHAPER_STRESS") != "1" {
		t.Skip("set SMUX_SHAPER_STRESS=1 to run concurrent shaper stress test")
	}

	const (
		streams           = 10
		requestsPerStream = 1000
	)

	sq := NewShaperQueue()
	start := make(chan struct{})
	var producers sync.WaitGroup
	producers.Add(streams)
	for sid := range streams {
		sid := sid
		go func() {
			defer producers.Done()
			<-start
			for seq := range requestsPerStream {
				sq.Push(writeRequest{
					frame: Frame{sid: uint32(sid)},
					seq:   uint32(seq),
				})
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		producers.Wait()
		close(done)
	}()

	nextSeq := make([]uint32, streams)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for total := 0; total < streams*requestsPerStream; {
		req, ok := sq.Pop()
		if ok {
			if int(req.frame.sid) >= streams {
				t.Fatalf("unexpected stream ID %d", req.frame.sid)
			}
			sid := int(req.frame.sid)
			if req.seq != nextSeq[sid] {
				t.Fatalf("stream %d sequence = %d; want %d", sid, req.seq, nextSeq[sid])
			}
			nextSeq[sid]++
			total++
			continue
		}

		select {
		case <-done:
			if sq.IsEmpty() {
				t.Fatalf("queue drained after %d of %d requests", total, streams*requestsPerStream)
			}
		case <-timer.C:
			t.Fatalf("timed out after popping %d of %d requests", total, streams*requestsPerStream)
		default:
			runtime.Gosched()
		}
	}
	<-done
	if !sq.IsEmpty() {
		t.Fatalf("queue has %d requests after expected drain", sq.Len())
	}
}

func TestShaperQueue_PopBoundary(t *testing.T) {
	sq := NewShaperQueue()

	// 1. Empty Queue
	if _, ok := sq.Pop(); ok {
		t.Fatal("Pop on empty queue should return false")
	}

	// 2. Single Stream Lifecycle
	// Push 2 items to Stream 10
	sq.Push(writeRequest{frame: Frame{sid: 10}, seq: 1})
	sq.Push(writeRequest{frame: Frame{sid: 10}, seq: 2})

	if sq.Len() != 2 {
		t.Fatalf("Expected len 2, got %d", sq.Len())
	}

	// Pop 1
	req, ok := sq.Pop()
	if !ok || req.frame.sid != 10 || req.seq != 1 {
		t.Fatalf("Expected sid 10 seq 1, got %v %v", req.frame.sid, req.seq)
	}
	// Check internals
	if len(sq.streams) != 1 {
		t.Errorf("Expected 1 stream in map, got %d", len(sq.streams))
	}
	if sq.rrList.Len() != 1 {
		t.Errorf("Expected 1 item in rrList, got %d", sq.rrList.Len())
	}

	// Pop 2 (Stream becomes empty)
	req, ok = sq.Pop()
	if !ok || req.frame.sid != 10 || req.seq != 2 {
		t.Fatalf("Expected sid 10 seq 2, got %v %v", req.frame.sid, req.seq)
	}
	// Check internals - should be cleaned up
	if len(sq.streams) != 0 {
		t.Errorf("Expected 0 streams in map, got %d", len(sq.streams))
	}
	if sq.rrList.Len() != 0 {
		t.Errorf("Expected 0 items in rrList, got %d", sq.rrList.Len())
	}
	if sq.next != nil {
		t.Errorf("Expected next to be nil, got %v", sq.next)
	}

	// Pop empty again
	if _, ok := sq.Pop(); ok {
		t.Fatal("Pop on empty queue should return false")
	}
}

func TestShaperQueue_MultiStreamRemoval(t *testing.T) {
	sq := NewShaperQueue()

	// Setup:
	// Stream 10: 1 item
	// Stream 20: 2 items
	// Stream 30: 1 item
	// Push order matters for Round Robin initial order if we push sequentially for new streams.
	// NewShaperQueue appends to list.
	// Order in list: 10, 20, 30

	sq.Push(writeRequest{frame: Frame{sid: 10}, seq: 1})
	sq.Push(writeRequest{frame: Frame{sid: 20}, seq: 1})
	sq.Push(writeRequest{frame: Frame{sid: 20}, seq: 2})
	sq.Push(writeRequest{frame: Frame{sid: 30}, seq: 1})

	// Current List: [10, 20, 30]
	// Next: 10

	// 1. Pop Stream 10 (seq 1). Stream 10 becomes empty and should be removed.
	// Next should move to 20.
	req, ok := sq.Pop()
	if !ok || req.frame.sid != 10 {
		t.Fatalf("Expected sid 10, got %v", req.frame.sid)
	}
	if _, exists := sq.streams[10]; exists {
		t.Error("Stream 10 should be removed")
	}
	if sq.rrList.Len() != 2 {
		t.Errorf("Expected list len 2, got %d", sq.rrList.Len())
	}
	// Current List: [20, 30] (conceptually, implementation might be linked list nodes)
	// Next should be 20.

	// 2. Pop Stream 20 (seq 1). Stream 20 has 1 left.
	// Next should move to 30.
	req, ok = sq.Pop()
	if !ok || req.frame.sid != 20 || req.seq != 1 {
		t.Fatalf("Expected sid 20 seq 1, got %v %v", req.frame.sid, req.seq)
	}
	if sq.rrList.Len() != 2 {
		t.Errorf("Expected list len 2, got %d", sq.rrList.Len())
	}

	// 3. Pop Stream 30 (seq 1). Stream 30 becomes empty and removed.
	// Next should wrap around to 20.
	req, ok = sq.Pop()
	if !ok || req.frame.sid != 30 {
		t.Fatalf("Expected sid 30, got %v", req.frame.sid)
	}
	if _, exists := sq.streams[30]; exists {
		t.Error("Stream 30 should be removed")
	}
	if sq.rrList.Len() != 1 {
		t.Errorf("Expected list len 1, got %d", sq.rrList.Len())
	}

	// 4. Pop Stream 20 (seq 2). Stream 20 becomes empty and removed.
	// List becomes empty.
	req, ok = sq.Pop()
	if !ok || req.frame.sid != 20 || req.seq != 2 {
		t.Fatalf("Expected sid 20 seq 2, got %v %v", req.frame.sid, req.seq)
	}
	if sq.rrList.Len() != 0 {
		t.Errorf("Expected list len 0, got %d", sq.rrList.Len())
	}
	if sq.next != nil {
		t.Error("Expected next to be nil")
	}
}

func TestShaperHeap_MemoryLeak(t *testing.T) {
	h := &shaperHeap{}
	heap.Init(h)

	req := writeRequest{frame: Frame{sid: 1, data: make([]byte, 100)}}
	heap.Push(h, req)

	if h.Len() != 1 {
		t.Fatal("Heap len should be 1")
	}
	backing := *h

	popped := heap.Pop(h).(writeRequest)
	if popped.frame.sid != 1 {
		t.Fatal("Incorrect popped item")
	}

	if h.Len() != 0 {
		t.Fatal("Heap len should be 0")
	}
	if popped.frame.data == nil || len(popped.frame.data) != len(req.frame.data) {
		t.Fatal("popped request should retain its returned payload")
	}
	if popped.frame.data[0] != req.frame.data[0] {
		t.Fatal("popped request payload unexpectedly changed")
	}
	if backing[0].frame.data != nil || backing[0].frame.sid != 0 {
		t.Fatal("heap retained a reference to the popped request")
	}
}

func TestShaperIsEmpty(t *testing.T) {
	sq := NewShaperQueue()
	if !sq.IsEmpty() {
		t.Fatal("ShaperQueue should be empty")
	}
	sq.Push(writeRequest{
		frame: newFrame(1, cmdPSH, 1),
	})
	if sq.IsEmpty() {
		t.Fatal("ShaperQueue should not be empty")
	}
}
