package smux

type Framer interface {
	// Split byte stream into frames
	Split(bts []byte) []Frame
}

// Framer is a frame splitter for byte stream
type DefaultFramer struct {
	maxFrameSize int
}

func newFramer(maxFrameSize int) *DefaultFramer {
	fr := new(DefaultFramer)
	fr.maxFrameSize = maxFrameSize
	return fr
}

func (fr *DefaultFramer) Split(bts []byte) (frames []Frame) {
	for len(bts) > fr.maxFrameSize {
		frame := Frame{}
		frame.payload = make([]byte, fr.maxFrameSize)
		n := copy(frame.payload, bts)
		bts = bts[n:]
		frames = append(frames, frame)
	}
	if len(bts) > 0 {
		frame := Frame{}
		frame.payload = make([]byte, len(bts))
		copy(frame.payload, bts)
		frames = append(frames, frame)
	}
	return nil
}
