package smux

type Frame struct {
	SessionId uint32
	Option    uint16
	FrameType uint8
	Payload   []byte
}
