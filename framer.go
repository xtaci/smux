package smux

import "time"

type Frame struct {
	SessionId uint32
	Option    uint16
	FrameType uint8
	Payload   []byte
	Timestamp time.Time
}

// serialize a frame to transmit
func (f *Framer) serialize() []byte {
	return nil
}

// deserialize a byte slice into current Frame
func (f *Framer) deserialize([]byte) {
	return nil
}
