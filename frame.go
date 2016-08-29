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
	cmdSYN  byte = iota // stream open
	cmdACK              // acknowledge stream open
	cmdFIN              // stream close
	cmdRST              // non-existent stream
	cmdWASK             // ask window
	cmdWINS             // window size
	cmdPSH              // data push
)

const (
	sizeOfVer    = 1
	sizeOfCmd    = 1
	sizeOfLength = 4
	sizeOfSid    = 4
	sizeOfUNA    = 4
	headerSize   = sizeOfVer + sizeOfCmd + sizeOfSid + sizeOfUNA + sizeOfLength
)

// Frame defines a packet from or to be multiplexed into a single connection
type Frame struct {
	ver  byte
	cmd  byte
	sid  uint32
	wnd  uint32
	data []byte
	ts   time.Time
}

// Serialize a frame to transmit
// VERSION(1B) | CMD(1B) | STREAMID(4B) | UNA(4B) | LENGTH(4B) | DATA  |
func Serialize(f *Frame) []byte {
	buf := make([]byte, headerSize+len(f.data))
	buf[0] = version
	buf[1] = f.cmd
	binary.LittleEndian.PutUint32(buf[2:], f.sid)
	binary.LittleEndian.PutUint32(buf[6:], f.wnd)
	binary.LittleEndian.PutUint32(buf[10:], uint32(len(f.data)))
	copy(buf[headerSize:], f.data)
	return buf
}

// Deserialize a byte slice into a frame
func Deserialize(bts []byte) *Frame {
	f := new(Frame)
	f.ver = bts[0]
	f.cmd = bts[1]
	f.sid = binary.LittleEndian.Uint32(bts[2:])
	f.wnd = binary.LittleEndian.Uint32(bts[6:])
	datalength := binary.LittleEndian.Uint32(bts[10:])
	if datalength > 0 {
		f.data = make([]byte, datalength)
		copy(f.data, bts[headerSize:])
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

func (h header) Wnd() uint32 {
	return binary.BigEndian.Uint32(h[6:])
}

func (h header) Length() uint32 {
	return binary.BigEndian.Uint32(h[10:])
}

func (h header) String() string {
	return fmt.Sprintf("Version:%d Cmd:%d StreamID:%d Wnd:%d Length:%d",
		h.Version(), h.Cmd(), h.StreamID(), h.Wnd(), h.Length())
}
