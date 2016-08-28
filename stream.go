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
	fr      DefaultFramer
	qdisc   Qdisc
}

func newStream(fr DefaultFramer, qdisc Qdisc) *Stream {
	stream := new(Stream)
	stream.fr = fr
	stream.qdisc = qdisc
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
