package main

import (
	"fmt"
	"io"
	"net"

	"github.com/pitrozx/smux"
)

func main() {
	// Create a listener and dial
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("Listen failed: %v\n", err)
		return
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

		session, err := smux.Server(conn, nil)
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
			serverDone <- fmt.Errorf("baseID mismatch: got %d, expected %d", baseID, expectedBaseID)
			return
		}

		fmt.Printf("Server: Received stream with baseID=%d ✓\n", baseID)

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
		fmt.Printf("Dial failed: %v\n", err)
		return
	}
	defer clientConn.Close()

	clientSession, err := smux.Client(clientConn, nil)
	if err != nil {
		fmt.Printf("Client failed: %v\n", err)
		return
	}
	defer clientSession.Close()

	// Test 1: Open stream with baseID
	baseID := uint32(12345)
	clientStream, err := clientSession.OpenStreamWithBaseID(baseID)
	if err != nil {
		fmt.Printf("OpenStreamWithBaseID failed: %v\n", err)
		return
	}
	defer clientStream.Close()

	// Verify the baseID is stored correctly on client side
	if clientStream.BaseID() != baseID {
		fmt.Printf("Client BaseID mismatch: got %d, expected %d\n", clientStream.BaseID(), baseID)
		return
	}
	fmt.Printf("Client: Opened stream with baseID=%d ✓\n", baseID)

	// Write some data
	_, err = clientStream.Write([]byte("hello"))
	if err != nil {
		fmt.Printf("Write failed: %v\n", err)
		return
	}

	// Read response
	buf := make([]byte, 8)
	_, err = io.ReadFull(clientStream, buf)
	if err != nil {
		fmt.Printf("Read failed: %v\n", err)
		return
	}

	fmt.Printf("Client: Received response: %s ✓\n", string(buf))

	// Wait for server
	if err := <-serverDone; err != nil {
		fmt.Printf("Server error: %v\n", err)
		return
	}

	fmt.Println("\n=== Test 1 PASSED: OpenStreamWithBaseID ===")

	// Test 2: Backward compatibility - OpenStream without baseID
	fmt.Println("\nTest 2: Backward Compatibility")

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("Listen failed: %v\n", err)
		return
	}
	defer ln2.Close()

	serverDone2 := make(chan error, 1)
	go func() {
		conn, err := ln2.Accept()
		if err != nil {
			serverDone2 <- err
			return
		}
		defer conn.Close()

		session, err := smux.Server(conn, nil)
		if err != nil {
			serverDone2 <- err
			return
		}
		defer session.Close()

		stream, err := session.AcceptStream()
		if err != nil {
			serverDone2 <- err
			return
		}
		defer stream.Close()

		if stream.BaseID() != 0 {
			serverDone2 <- fmt.Errorf("baseID should be 0, got %d", stream.BaseID())
			return
		}

		fmt.Printf("Server: Received stream with baseID=0 (traditional OpenStream) ✓\n")

		// Read and write back
		buf := make([]byte, 5)
		_, err = io.ReadFull(stream, buf)
		if err != nil {
			serverDone2 <- err
			return
		}

		_, err = stream.Write([]byte("ok"))
		if err != nil {
			serverDone2 <- err
			return
		}

		serverDone2 <- nil
	}()

	clientConn2, err := net.Dial("tcp", ln2.Addr().String())
	if err != nil {
		fmt.Printf("Dial failed: %v\n", err)
		return
	}
	defer clientConn2.Close()

	clientSession2, err := smux.Client(clientConn2, nil)
	if err != nil {
		fmt.Printf("Client failed: %v\n", err)
		return
	}
	defer clientSession2.Close()

	// Use traditional OpenStream
	clientStream2, err := clientSession2.OpenStream()
	if err != nil {
		fmt.Printf("OpenStream failed: %v\n", err)
		return
	}
	defer clientStream2.Close()

	fmt.Printf("Client: Opened stream with traditional OpenStream() ✓\n")

	// Write data
	_, err = clientStream2.Write([]byte("hello"))
	if err != nil {
		fmt.Printf("Write failed: %v\n", err)
		return
	}

	// Read response
	buf = make([]byte, 2)
	_, err = io.ReadFull(clientStream2, buf)
	if err != nil {
		fmt.Printf("Read failed: %v\n", err)
		return
	}

	fmt.Printf("Client: Received response: %s ✓\n", string(buf))

	// Wait for server
	if err := <-serverDone2; err != nil {
		fmt.Printf("Server error: %v\n", err)
		return
	}

	fmt.Println("\n=== Test 2 PASSED: Backward Compatibility ===")
	fmt.Println("\n✓ All tests PASSED!")
}
