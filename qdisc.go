package smux

import "sync"

// Qdisc defines the interface for queue discipline
type Qdisc interface {
	// Enqueue puts a frame into the queue
	Enqueue(f Frame)
	// Dequeue returns the next frame to be sent
	Dequeue() Frame
	// Count returns the size of the queue
	Count() int
}

type FIFO struct {
	q  []Frame
	mu sync.Mutex
}

func newFIFOQdisc() *FIFO {
	fifo := new(FIFO)
	return fifo
}

func (fifo *FIFO) Enqueue(f Frame) {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	fifo.q = append(fifo.q, f)
}

func (fifo *FIFO) Dequeue() (f Frame) {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	f = fifo.q[0]
	fifo.q = fifo.q[1:]
	return
}

func (fifo *FIFO) Count() int {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	return len(fifo.q)
}
