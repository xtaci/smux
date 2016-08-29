package smux

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	version = 1
)

const ( // cmds
	cmdSYN byte = iota
	cmdACK
	cmdFIN
	cmdRST
	cmdWND
)

const (
	sizeOfVersion    = 1
	sizeOfCmd        = 1
	sizeOfDataLength = 4
	sizeOfStreamId   = 4
	sizeOfUNA        = 4
	headerSize       = sizeOfVersion + sizeOfCmd + sizeOfStreamId + sizeOfUNA + sizeOfDataLength
)

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	version  byte
	cmd      byte
	streamId uint32
	una      uint32
	payload  []byte
	ts       time.Time
}

// Serialize a frame to transmit
// VERSION(1B) | CMD(1B) | STREAMID(4B) | UNA(4B) | DATALENGTH(4B) | DATA  |
func Serialize(f *Frame) []byte {
	buf := make([]byte, headerSize+len(f.payload))
	buf[0] = version
	buf[1] = f.cmd
	binary.LittleEndian.PutUint32(buf[2:], f.streamId)
	binary.LittleEndian.PutUint32(buf[6:], f.una)
	binary.LittleEndian.PutUint32(buf[10:], uint32(len(f.payload)))
	copy(buf[headerSize:], f.payload)
	return buf
}

// Deserialize a byte slice into a frame
func Deserialize(bts []byte) *Frame {
	f := new(Frame)
	f.version = bts[0]
	f.cmd = bts[1]
	f.streamId = binary.LittleEndian.Uint32(bts[2:])
	f.una = binary.LittleEndian.Uint32(bts[6:])
	datalength := binary.LittleEndian.Uint32(bts[10:])
	if datalength > 0 {
		f.payload = make([]byte, datalength)
		copy(f.payload, bts[headerSize:])
	}
	return f
}

type header []byte

func (h header) Version() byte {
	return h[0]
}

func (h header) Cmd() byte {
	return h[1]
}

func (h header) StreamID() uint32 {
	return binary.BigEndian.Uint32(h[2:])
}

func (h header) UNA() uint32 {
	return binary.BigEndian.Uint32(h[6:])
}

func (h header) Length() uint32 {
	return binary.BigEndian.Uint32(h[10:])
}

func (h header) String() string {
	return fmt.Sprintf("Version:%d Cmd:%d StreamID:%d UNA:%d Length:%d",
		h.Version(), h.Cmd(), h.StreamID(), h.UNA(), h.Length())
}
