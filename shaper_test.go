package smux

import (
	"container/heap"
	"testing"
)

func TestShaper(t *testing.T) {
	w1 := writeRequest{prio: 10}
	w2 := writeRequest{prio: 10}
	w3 := writeRequest{prio: 20}
	w4 := writeRequest{prio: 100}
	w5 := writeRequest{prio: (1 << 32) - 1}

	var reqs shaperHeap
	heap.Push(&reqs, w5)
	heap.Push(&reqs, w4)
	heap.Push(&reqs, w3)
	heap.Push(&reqs, w2)
	heap.Push(&reqs, w1)

	var lastPrio = reqs[0].prio
	for len(reqs) > 0 {
		w := heap.Pop(&reqs).(writeRequest)
		if int32(w.prio-lastPrio) < 0 {
			t.Fatal("incorrect shaper priority")
		}

		t.Log("prio:", w.prio)
		lastPrio = w.prio
	}
}

func TestShaper2(t *testing.T) {
	w1 := writeRequest{prio: 10, seq: 1} // stream 0
	w2 := writeRequest{prio: 10, seq: 2}
	w3 := writeRequest{prio: 20, seq: 3}
	w4 := writeRequest{prio: 100, seq: 4}
	w5 := writeRequest{prio: (1 << 32) - 1, seq: 5}
	w6 := writeRequest{prio: 10, seq: 1, frame: Frame{sid: 10}} // stream 1

	var reqs shaperHeap
	heap.Push(&reqs, w6)
	heap.Push(&reqs, w5)
	heap.Push(&reqs, w4)
	heap.Push(&reqs, w3)
	heap.Push(&reqs, w2)
	heap.Push(&reqs, w1)

	for len(reqs) > 0 {
		w := heap.Pop(&reqs).(writeRequest)
		t.Log("prio:", w.prio, "sid:", w.frame.sid, "seq:", w.seq)
	}
}
