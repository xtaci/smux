package smux

const ( // frame types
	frameData uint8 = iota
	frameWindowUpdate
	framePing
	frameGoAway
)

const ( // options
	flagSYN uint16 = 1 << iota
	flagACK
	flagFIN
	flagRST
)
