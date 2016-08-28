package smux

import "time"

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	SessionId uint32
	Option    uint16
	FrameType uint8
	Payload   []byte
	Timestamp time.Time
}

// Framer is a frame splitter for byte stream
type Framer struct {
}

// split bytestream into frames
func (fr *Framer) split(bts []byte) []*Frame {
}

// serialize a frame to transmit
func (fr *Framer) serialize(f *Frame) []byte {
	return nil
}

// deserialize a byte slice into a frame
func (fr *Framer) deserialize(bts []byte) *Frame {
	return nil
}
