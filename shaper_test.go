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
	"testing"
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
