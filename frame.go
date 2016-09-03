package smux

import (
	"encoding/binary"
	"fmt"
)

const (
	version = 1
)

const ( // cmds
	cmdSYN       byte = iota // stream open
	cmdRST                   // stream close
	cmdPSH                   // data push
	cmdNOP                   // no operation
	cmdTerminate             // explict terminate session
)

const (
	sizeOfVer    = 1
	sizeOfCmd    = 1
	sizeOfLength = 2
	sizeOfSid    = 4
	headerSize   = 8 //sizeOfVer + sizeOfCmd + sizeOfSid + sizeOfLength
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

// MarshalBinary a frame to transmit
// VERSION(1B) | CMD(1B) | LENGTH(2B) | STREAMID(4B) | DATA  |
func (f *Frame) MarshalBinary() ([]byte, error) {
	buf := make([]byte, headerSize+len(f.data))
	buf[0] = version
	buf[1] = f.cmd
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(f.data)))
	binary.LittleEndian.PutUint32(buf[4:], f.sid)
	copy(buf[headerSize:], f.data)
	return buf, nil
}

// UnmarshalBinary a byte slice into a frame
func (f *Frame) UnmarshalBinary(bts []byte) error {
	f.ver = bts[0]
	f.cmd = bts[1]
	datalength := binary.LittleEndian.Uint16(bts[2:])
	f.sid = binary.LittleEndian.Uint32(bts[4:])
	if datalength > 0 {
		f.data = make([]byte, datalength)
		copy(f.data, bts[headerSize:])
	}
	return nil
}

type rawHeader []byte

func (h rawHeader) Version() byte {
	return h[0]
}

func (h rawHeader) Cmd() byte {
	return h[1]
}

func (h rawHeader) Length() uint16 {
	return binary.LittleEndian.Uint16(h[2:])
}

func (h rawHeader) StreamID() uint32 {
	return binary.LittleEndian.Uint32(h[4:])
}

func (h rawHeader) String() string {
	return fmt.Sprintf("Version:%d Cmd:%d StreamID:%d Length:%d",
		h.Version(), h.Cmd(), h.StreamID(), h.Length())
}
