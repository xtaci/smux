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
	sizeOfVersion     = 1
	sizeOfFrameType   = 1
	sizeOfOptions     = 2
	sizeOfDataLength  = 4
	sizeOfStreamId    = 4
	sizeOfWindow      = 4
	headerSizeData    = sizeOfVersion + sizeOfFrameType + sizeOfOptions + sizeOfStreamId + sizeOfDataLength
	headerSizeControl = sizeOfVersion + sizeOfFrameType + sizeOfOptions + sizeOfDataLength
)

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	streamId  uint32
	frameType uint8
	options   uint8
	payload   []byte
	window    uint32
	pingId    uint32
	ts        time.Time
}

// Serialize a frame to transmit
func Serialize(f *Frame) []byte {
	switch f.frameType {
	case frameData:
	case frameControl:
	}

	return nil
}

// VERSION(1B) | FRAMETYPE(1B) | OPTIONS(2B)  | STREAMID(4B) | WINDOW(4B) | DATALENGTH(4B) | DATA  |
func serializeFrameData(f *Frame) []byte {
	buf := make([]byte, headerSizeData+len(f.payload))
	buf[0] = version
	buf[1] = frameData
	binary.LittleEndian.PutUint16(buf[2:], uint16(f.options))
	binary.LittleEndian.PutUint32(buf[4:], f.streamId)
	binary.LittleEndian.PutUint32(buf[8:], f.window)
	binary.LittleEndian.PutUint32(buf[12:], uint32(len(f.payload)))
	copy(buf[headerSizeData:], f.payload)
	return buf
}

// VERSION(1B) | FRAMETYPE(1B) | OPTIONS(2B)  | WINDOW(4B) | DATALENGTH(4B) | DATA  |
func serializeFrameControl(f *Frame) []byte {
	buf := make([]byte, headerSizeData+len(f.payload))
	buf[0] = version
	buf[1] = frameControl
	binary.LittleEndian.PutUint16(buf[2:], uint16(f.options))
	binary.LittleEndian.PutUint32(buf[4:], f.window)
	return buf
}

// Deserialize a byte slice into a frame
func Deserialize(bts []byte) *Frame {
	return nil
}
