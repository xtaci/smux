// MIT License
//
// Copyright (c) 2016-2017 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package smux

import (
	"io"
	"net"
	"testing"
)

// TestOpenStreamWithBaseID tests the OpenStreamWithBaseID functionality
func TestOpenStreamWithBaseID(t *testing.T) {
	// Create a listener and dial
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	// Server side
	serverDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()

		session, err := Server(conn, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer session.Close()

		// Accept the stream that was opened with baseID
		stream, err := session.AcceptStream()
		if err != nil {
			serverDone <- err
			return
		}
		defer stream.Close()

		// Check if the baseID was correctly received
		baseID := stream.BaseID()
		expectedBaseID := uint32(12345)
		if baseID != expectedBaseID {
			serverDone <- &testError{
				msg: "baseID mismatch",
				got: baseID,
				exp: expectedBaseID,
			}
			return
		}

		// Write some data back
		_, err = stream.Write([]byte("response"))
		if err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	// Client side
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	clientSession, err := Client(clientConn, nil)
	if err != nil {
		t.Fatalf("Client failed: %v", err)
	}
	defer clientSession.Close()

	// Open stream with baseID
	baseID := uint32(12345)
	clientStream, err := clientSession.OpenStreamWithBaseID(baseID)
	if err != nil {
		t.Fatalf("OpenStreamWithBaseID failed: %v", err)
	}
	defer clientStream.Close()

	// Verify the baseID is stored correctly on client side
	if clientStream.BaseID() != baseID {
		t.Errorf("Client BaseID mismatch: got %d, expected %d", clientStream.BaseID(), baseID)
	}

	// Write some data
	_, err = clientStream.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read response
	buf := make([]byte, 8)
	_, err = io.ReadFull(clientStream, buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Wait for server
	if err := <-serverDone; err != nil {
		t.Fatalf("Server error: %v", err)
	}
}

// TestOpenStreamWithBaseIDZero tests that baseID 0 works the same as OpenStream
func TestOpenStreamWithBaseIDZero(t *testing.T) {
	// Create a listener and dial
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	// Server side
	serverDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()

		session, err := Server(conn, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer session.Close()

		// Accept the stream
		stream, err := session.AcceptStream()
		if err != nil {
			serverDone <- err
			return
		}
		defer stream.Close()

		// Check if the baseID is 0 (no baseID was set)
		if stream.BaseID() != 0 {
			serverDone <- &testError{
				msg: "baseID should be 0",
				got: stream.BaseID(),
				exp: uint32(0),
			}
			return
		}

		serverDone <- nil
	}()

	// Client side
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	clientSession, err := Client(clientConn, nil)
	if err != nil {
		t.Fatalf("Client failed: %v", err)
	}
	defer clientSession.Close()

	// Open stream with baseID 0 (should behave like OpenStream)
	clientStream, err := clientSession.OpenStreamWithBaseID(0)
	if err != nil {
		t.Fatalf("OpenStreamWithBaseID(0) failed: %v", err)
	}
	defer clientStream.Close()

	// Verify the baseID is 0
	if clientStream.BaseID() != 0 {
		t.Errorf("Client BaseID should be 0, got %d", clientStream.BaseID())
	}

	// Write some data
	_, err = clientStream.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Wait for server
	if err := <-serverDone; err != nil {
		t.Fatalf("Server error: %v", err)
	}
}

// TestOpenStreamBackwardCompatibility tests that OpenStream still works
func TestOpenStreamBackwardCompatibility(t *testing.T) {
	// Create a listener and dial
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	// Server side
	serverDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()

		session, err := Server(conn, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer session.Close()

		// Accept the stream
		stream, err := session.AcceptStream()
		if err != nil {
			serverDone <- err
			return
		}
		defer stream.Close()

		// Read data
		buf := make([]byte, 5)
		_, err = io.ReadFull(stream, buf)
		if err != nil {
			serverDone <- err
			return
		}

		// Write response
		_, err = stream.Write([]byte("ok"))
		if err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	// Client side
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientConn.Close()

	clientSession, err := Client(clientConn, nil)
	if err != nil {
		t.Fatalf("Client failed: %v", err)
	}
	defer clientSession.Close()

	// Use traditional OpenStream
	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	defer clientStream.Close()

	// Write data
	_, err = clientStream.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read response
	buf := make([]byte, 2)
	_, err = io.ReadFull(clientStream, buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Wait for server
	if err := <-serverDone; err != nil {
		t.Fatalf("Server error: %v", err)
	}
}

// testError is a simple error type for testing
type testError struct {
	msg string
	got interface{}
	exp interface{}
}

func (e *testError) Error() string {
	return e.msg
}
