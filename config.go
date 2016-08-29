package smux

type Config struct {
	Framer       Framer
	Qdisc        Qdisc
	MaxFrameSize uint16
}
