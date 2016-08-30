package smux

type Framer struct {
	maxFrameSize int
	// Split byte stream into frames
}

func newFramer(maxFrameSize int) *Framer {
	fr := new(Framer)
	fr.maxFrameSize = maxFrameSize
	return fr
}

func (fr *Framer) Split(bts []byte, cmd byte, sid uint32) (frames []Frame) {
	for len(bts) > fr.maxFrameSize {
		frame := newFrame(cmd, sid)
		frame.data = make([]byte, fr.maxFrameSize)
		n := copy(frame.data, bts)
		bts = bts[n:]
		frames = append(frames, frame)
	}
	if len(bts) > 0 {
		frame := newFrame(cmd, sid)
		frame.data = make([]byte, len(bts))
		copy(frame.data, bts)
		frames = append(frames, frame)
	}
	return nil
}
