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
	"fmt"
	"math/rand"
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

	for len(reqs) > 0 {
		w := heap.Pop(&reqs).(writeRequest)
		t.Log("sid:", w.frame.sid, "seq:", w.seq)
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

	for len(reqs) > 0 {
		w := heap.Pop(&reqs).(writeRequest)
		t.Log("sid:", w.frame.sid, "seq:", w.seq)
	}
}

func TestShaperQueueFairness(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	sq := NewShaperQueue()

	const streams = 10
	const testDuration = 10 * time.Second

	var wg sync.WaitGroup
	sendCount := make([]uint64, streams)

	stop := make(chan struct{})

	// Producers: each stream pushes packets
	for sid := 0; sid < streams; sid++ {
		sid := sid
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq := uint32(0)
			for {
				select {
				case <-stop:
					return
				default:
				}
				sq.Push(writeRequest{
					frame: Frame{sid: uint32(sid)},
					seq:   seq,
				})
				seq++
				time.Sleep(time.Duration(rand.Intn(300)) * time.Microsecond)
			}
		}()
	}

	// Consumer: slow network, 1 pop every 10ms
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				req, ok := sq.Pop()
				if ok {
					sendCount[req.frame.sid]++
				}
			}
		}
	}()

	// ---- NEW: periodic live report ----
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fmt.Printf("[DEBUG] Current counts: %v\n", sendCount)
			}
		}
	}()

	// run test
	time.Sleep(testDuration)
	close(stop)
	wg.Wait()

	// ---- final report ----
	fmt.Println("=== FINAL COUNTS ===")
	fmt.Println(sendCount)

	// ---- fairness check ----
	total := uint64(0)
	for _, c := range sendCount {
		total += c
	}
	avg := total / streams
	tolerance := avg / 4 // 25%

	for sid, c := range sendCount {
		if c < avg-tolerance || c > avg+tolerance {
			t.Errorf("stream %d unfair: got %d, avg %d", sid, c, avg)
		}
	}
}

func TestShaperQueue_FastWriteSlowRead(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	const (
		streams      = 10
		duration     = 10 * time.Second
		producerWait = 1 * time.Microsecond  // super fast writing
		consumerWait = 15 * time.Millisecond // super slow reading
	)

	sq := NewShaperQueue()

	sendCount := make([]uint64, streams)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Producers: extremely fast writers
	for sid := 0; sid < streams; sid++ {
		sid := sid
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq := uint32(0)
			for {
				select {
				case <-stop:
					return
				default:
				}

				sq.Push(writeRequest{
					frame: Frame{sid: uint32(sid)},
					seq:   seq,
				})
				seq++
				time.Sleep(producerWait)
			}
		}()
	}

	// Consumer: very slow reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			req, ok := sq.Pop()
			if ok {
				sendCount[req.frame.sid]++
			}

			time.Sleep(consumerWait)
		}
	}()

	// Periodic monitor
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fmt.Printf("[DEBUG] Queue size=%d, counts=%v\n", sq.count, sendCount)
			}
		}
	}()

	// Run test
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	fmt.Printf("=== FINAL ===\ncounts=%v\nqueue remaining=%d\n", sendCount, sq.count)

	// Check fairness
	total := uint64(0)
	for _, v := range sendCount {
		total += v
	}
	avg := total / streams
	tolerance := avg / 3 // allow 33%

	for sid, c := range sendCount {
		if c < avg-tolerance || c > avg+tolerance {
			t.Errorf("stream %d unfair: got %d, avg %d", sid, c, avg)
		}
	}
}
