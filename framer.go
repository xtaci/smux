package smux

import "time"

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	StreamId  uint32
	Option    uint16
	FrameType uint8
	Payload   []byte
	Timestamp time.Time
}

// Serialize a frame to transmit
func Serialize(f *Frame) []byte {
	return nil
}

// Deserialize a byte slice into a frame
func Deserialize(bts []byte) *Frame {
	return nil
}

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
