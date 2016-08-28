package smux

const (
	STREAM_IDLE = 1 << iota
	STREAM_NEW
	STREAM_ESTABLISHED
	STREAM_CLOSED
)

// Stream implements io.ReadWriteCloser
type Stream struct {
	state   int
	rxQueue []*Frame // receive queue
	config  *Config
}

func newStream(config *Config) *Stream {
	stream := new(Stream)
	stream.config = config
	return stream
}

func (s *Stream) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (s *Stream) Write(b []byte) (n int, err error) {
	return 0, nil
}

func (s *Stream) Close() error {
	return nil
}
