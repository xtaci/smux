package smux

import "sync"

// Qdisc defines the interface for queue discipline
type Qdisc interface {
	// Enqueue puts a frame into the queue
	Enqueue(f Frame)
	// Dequeue returns the next frame to be sent
	Dequeue() Frame
	// IsEmpty checks whether the queue is empty
	IsEmpty() bool
	// Count returns the size of the queue
	Count() int
	// Capacity returns the capacity of the queue
	Capacity() int
}

type FIFO struct {
	q   []Frame
	cap int
	mu  sync.Mutex
}

func newFIFOQdisc(cap int) *FIFO {
	fifo := new(FIFO)
	fifo.cap = cap
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

func (fifo *FIFO) IsEmpty() bool {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	if fifo.q == nil || len(fifo.q) == 0 {
		return true
	}
	return false
}

func (fifo *FIFO) Count() int {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	return len(fifo.q)
}

func (fifo *FIFO) Capacity() int {
	fifo.mu.Lock()
	defer fifo.mu.Unlock()
	return fifo.cap
}
