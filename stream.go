package smux

const (
	STREAM_IDLE = 1 << iota
	STREAM_NEW
	STREAM_ESTABLISHED
	STREAM_CLOSED
)

type Stream struct {
	state  int
	config *Config
}

func newStream(config *Config) *Stream {
	stream := new(Stream)
	stream.config = config
	return stream
}
