package smux

import "time"

type Config struct {
	Framer            Framer
	Qdisc             Qdisc
	MaxFrameSize      uint16
	StreamOpenTimeout time.Duration // sets the stream open timeout
}
