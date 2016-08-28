package smux

// Qdisc defines the interface for queue discipline
type Qdisc interface {
	// Enqueue puts a frame into the queue
	Enqueue(f *Frame)
	// Dequeue returns the next frame to be sent
	Dequeue() *Frame
	// IsEmpty checks whether the queue is empty
	IsEmpty() bool
	// Count returns the size of the queue
	Count() int
}

type FIFO struct {
	q []*Frame
}

func (fifo *FIFO) Enqueue(f *Frame) {
	fifo.q = append(fifo.q, f)
}

func (fifo *FIFO) Dequeue() (f *Frame) {
	f = fifo.q[0]
	fifo.q = fifo.q[1:]
	return
}

func (fifo *FIFO) IsEmpty() bool {
	if fifo.q == nil || len(fifo.q) == 0 {
		return true
	}
	return false
}

func (fifo *FIFO) Count() int {
	return len(fifo.q)
}
