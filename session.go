package smux

import "io"

type Session struct {
	conn         io.ReadWriteCloser
	nextStreamID uint32
	config       *Config
	streams      map[uint32]*Stream
}

func newSession(config *Config, conn io.ReadWriteCloser, client bool) *Session {
	s := new(Session)
	s.config = config
	s.conn = conn
	s.streams = make(map[uint32]*Stream)
	return s
}
