package smux

import "io"

// Server is used to initialize a new server-side connection.
func Server(conn io.ReadWriteCloser, maxframes int, framesize uint16) (*Session, error) {
	return newSession(conn, false, maxframes, framesize), nil
}

// Client is used to initialize a new client-side connection.
func Client(conn io.ReadWriteCloser, maxframes int, framesize uint16) (*Session, error) {
	return newSession(conn, true, maxframes, framesize), nil
}
