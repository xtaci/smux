package smux

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/pkg/errors"
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
	version   byte
	frameType byte
	streamId  uint32
	una       uint32
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
func Deserialize(bts []byte) (*Frame, error) {
	f := new(Frame)
	f.version = bts[0]
	f.frameType = bts[1]
	f.options = binary.LittleEndian.Uint16(bts[2:])
	f.streamId = binary.LittleEndian.Uint32(bts[4:])
	f.una = binary.LittleEndian.Uint32(bts[8:])
	datalength := binary.LittleEndian.Uint32(bts[12:])
	if datalength != uint32(len(bts[headerSize:])) {
		return nil, errors.New("frame format error")
	}
	if datalength > 0 {
		f.payload = make([]byte, datalength)
		copy(f.payload, bts[headerSize:])
	}
	return f, nil
}

type header []byte

func (h header) Version() byte {
	return h[0]
}

func (h header) FrameType() byte {
	return h[1]
}

func (h header) Options() uint16 {
	return binary.BigEndian.Uint16(h[2:4])
}

func (h header) StreamID() uint32 {
	return binary.BigEndian.Uint32(h[4:8])
}

func (h header) UNA() uint32 {
	return binary.BigEndian.Uint32(h[8:12])
}

func (h header) Length() uint32 {
	return binary.BigEndian.Uint32(h[12:14])
}

func (h header) String() string {
	return fmt.Sprintf("Version:%d FrameType:%d Options:%d StreamID:%d UNA:%d Length:%d",
		h.Version(), h.FrameType(), h.Options(), h.StreamID(), h.UNA(), h.Length())
}
