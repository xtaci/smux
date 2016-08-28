package smux

// TC defines the interface for traffic control
type TC interface {
	// Next returns the next frame to be sent
	Next() *Frame
	// Input puts a frame into the queue
	Input(f *Frame)
	// IsEmpty checks whether the queue is empty
	IsEmpty() bool
	// Count returns the size of the queue
	Count() int
}

type FIFO struct {
	q []*Frame
}

func (fifo *FIFO) Input(f *Frame) {
	fifo.q = append(fifo.q, f)
}

func (fifo *FIFO) Next() (f *Frame) {
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
