package smux

type Framer interface {
	// Split byte stream into frames
	Split(bts []byte) []Frame
}

// Framer is a frame splitter for byte stream
type DefaultFramer struct {
	streamId uint32
}

func newFramer(streamId uint32) *DefaultFramer {
	fr := new(DefaultFramer)
	fr.streamId = streamId
	return fr
}

func (fr *DefaultFramer) Split(bts []byte) []Frame {
	return nil
}
