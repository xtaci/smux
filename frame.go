package smux

import (
	"encoding/binary"
	"time"
)

const (
	version = 1
)
const ( // frame types
	frameData uint8 = iota
	frameControl
)

const ( // options
	flagSYN uint16 = 1 << iota
	flagACK
	flagFIN
	flagRST
)

const (
	sizeOfVersion    = 1
	sizeOfFrameType  = 1
	sizeOfOptions    = 2
	sizeOfDataLength = 4
	sizeOfStreamId   = 4
	sizeOfUNA        = 4
	headerSize       = sizeOfVersion + sizeOfFrameType + sizeOfOptions + sizeOfStreamId + sizeOfUNA + sizeOfDataLength
)

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	streamId  uint32
	una       uint32
	frameType uint8
	options   uint16
	payload   []byte
	ts        time.Time
}

// Serialize a frame to transmit
// VERSION(1B) | FRAMETYPE(1B) | OPTIONS(2B)  | STREAMID(4B) | UNA(4B) | DATALENGTH(4B) | DATA  |
func Serialize(f *Frame) []byte {
	buf := make([]byte, headerSize+len(f.payload))
	buf[0] = version
	buf[1] = f.frameType
	binary.LittleEndian.PutUint16(buf[2:], uint16(f.options))
	binary.LittleEndian.PutUint32(buf[4:], f.streamId)
	binary.LittleEndian.PutUint32(buf[8:], f.una)
	binary.LittleEndian.PutUint32(buf[12:], uint32(len(f.payload)))
	copy(buf[headerSize:], f.payload)
	return buf
}

// Deserialize a byte slice into a frame
func Deserialize(bts []byte) *Frame {
	return nil
}
