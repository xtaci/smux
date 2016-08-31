package smux

import "io"

func Server(conn io.ReadWriteCloser, maxframes, framesize int) (*Session, error) {
	return newSession(conn, false, maxframes, framesize), nil
}

func Client(conn io.ReadWriteCloser, maxframes, framesize int) (*Session, error) {
	return newSession(conn, true, maxframes, framesize), nil
}
