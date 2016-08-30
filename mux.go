package smux

import "io"

func Server(conn io.ReadWriteCloser) (*Session, error) {
	return newSession(4096, conn, false), nil
}

func Client(conn io.ReadWriteCloser) (*Session, error) {
	return newSession(4096, conn, true), nil
}
