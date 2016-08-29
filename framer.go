package smux

import "time"

type Framer interface {
	// Split byte stream into frames
	Split(bts []byte) []Frame
}

// Framer is a frame splitter for byte stream
type DefaultFramer struct {
	frameId      uint32
	maxFrameSize int
}

func newFramer(maxFrameSize int) *DefaultFramer {
	fr := new(DefaultFramer)
	fr.maxFrameSize = maxFrameSize
	return fr
}

func (fr *DefaultFramer) Split(bts []byte) (frames []Frame) {
	ts := time.Now()
	for len(bts) > fr.maxFrameSize {
		frame := Frame{}
		frame.payload = make([]byte, fr.maxFrameSize)
		frame.ts = ts
		n := copy(frame.payload, bts)
		bts = bts[n:]
	}
	return nil
}
