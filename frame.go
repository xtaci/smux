package smux

import (
	"encoding/binary"
	"fmt"
)

const (
	version = 1
)

const ( // cmds
	cmdSYN byte = iota // stream open
	cmdRST             // non-existent stream
	cmdPSH             // data push
)

const (
	sizeOfVer    = 1
	sizeOfCmd    = 1
	sizeOfLength = 4
	sizeOfSid    = 4
	headerSize   = sizeOfVer + sizeOfCmd + sizeOfSid + sizeOfLength
)

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	ver  byte
	cmd  byte
	sid  uint32
	data []byte
}

func newFrame(cmd byte, sid uint32) Frame {
	return Frame{ver: version, cmd: cmd, sid: sid}
}

// Marshal a frame to transmit
// VERSION(1B) | CMD(1B) | STREAMID(4B) | LENGTH(4B) | DATA  |
func (f *Frame) MarshalBinary() ([]byte, error) {
	buf := make([]byte, headerSize+len(f.data))
	buf[0] = version
	buf[1] = f.cmd
	binary.LittleEndian.PutUint32(buf[2:], f.sid)
	binary.LittleEndian.PutUint32(buf[6:], uint32(len(f.data)))
	copy(buf[headerSize:], f.data)
	return buf, nil
}

// Unmarshal a byte slice into a frame
func Unmarshal(bts []byte) Frame {
	f := Frame{}
	f.ver = bts[0]
	f.cmd = bts[1]
	f.sid = binary.LittleEndian.Uint32(bts[2:])
	datalength := binary.LittleEndian.Uint32(bts[6:])
	if datalength > 0 {
		f.data = make([]byte, datalength)
		copy(f.data, bts[headerSize:])
	}
	return f
}

type RawHeader []byte

func (h RawHeader) Version() byte {
	return h[0]
}

func (h RawHeader) Cmd() byte {
	return h[1]
}

func (h RawHeader) StreamID() uint32 {
	return binary.LittleEndian.Uint32(h[2:])
}

func (h RawHeader) Length() uint32 {
	return binary.LittleEndian.Uint32(h[6:])
}

func (h RawHeader) String() string {
	return fmt.Sprintf("Version:%d Cmd:%d StreamID:%d Length:%d",
		h.Version(), h.Cmd(), h.StreamID(), h.Length())
}
