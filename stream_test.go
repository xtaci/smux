package smux

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestBufferRingPushPopOrder(t *testing.T) {
	r := newBufferRing(2)
	b1 := []byte{1}
	b2 := []byte{2}
	b3 := []byte{3}
	r.push(b1, &b1)
	r.push(b2, &b2)

	buf, head, ok := r.pop()
	if !ok || buf[0] != 1 || head == nil || (*head)[0] != 1 {
		t.Fatalf("unexpected pop result: ok=%v buf=%v head=%v", ok, buf, head)
	}

	r.push(b3, &b3)

	buf, head, ok = r.pop()
	if !ok || buf[0] != 2 || head == nil || (*head)[0] != 2 {
		t.Fatalf("unexpected pop result after wrap: ok=%v buf=%v head=%v", ok, buf, head)
	}
	buf, head, ok = r.pop()
	if !ok || buf[0] != 3 || head == nil || (*head)[0] != 3 {
		t.Fatalf("unexpected pop result after wrap 2: ok=%v buf=%v head=%v", ok, buf, head)
	}
	if r.size != 0 || r.head != r.tail {
		t.Fatalf("ring not empty after pops: size=%d head=%d tail=%d", r.size, r.head, r.tail)
	}
}

func TestBufferRingGrow(t *testing.T) {
	r := newBufferRing(2)
	b1 := []byte{1}
	b2 := []byte{2}
	b3 := []byte{3}
	r.push(b1, &b1)
	r.push(b2, &b2)
	r.push(b3, &b3) // trigger grow

	if len(r.bufs) < 3 {
		t.Fatalf("expected ring to grow, capacity=%d", len(r.bufs))
	}

	buf, head, ok := r.pop()
	if !ok || buf[0] != 1 || head == nil || (*head)[0] != 1 {
		t.Fatalf("unexpected pop after grow: ok=%v buf=%v head=%v", ok, buf, head)
	}
	buf, head, ok = r.pop()
	if !ok || buf[0] != 2 || head == nil || (*head)[0] != 2 {
		t.Fatalf("unexpected pop after grow 2: ok=%v buf=%v head=%v", ok, buf, head)
	}
	buf, head, ok = r.pop()
	if !ok || buf[0] != 3 || head == nil || (*head)[0] != 3 {
		t.Fatalf("unexpected pop after grow 3: ok=%v buf=%v head=%v", ok, buf, head)
	}
	if r.size != 0 || r.head != r.tail {
		t.Fatalf("ring not empty after grow pops: size=%d head=%d tail=%d", r.size, r.head, r.tail)
	}
}

func TestBufferRingEmptyPop(t *testing.T) {
	r := newBufferRing(2)
	if buf, head, ok := r.pop(); ok || buf != nil || head != nil {
		t.Fatalf("expected empty pop, got ok=%v buf=%v head=%v", ok, buf, head)
	}
}

// setupHalfCloseServer creates a server that accepts streams and can be controlled for half-close tests
func setupHalfCloseServer(t *testing.T) (addr string, stopfunc func(), client net.Conn, serverSession chan *Session, err error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, nil, nil, err
	}
	serverSession = make(chan *Session, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sess, _ := Server(conn, nil)
		serverSession <- sess
	}()
	addr = ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		return "", nil, nil, nil, err
	}
	return ln.Addr().String(), func() { ln.Close() }, conn, serverSession, nil
}

// TestHalfCloseBasic tests that CloseWrite sends FIN but allows reading
func TestHalfCloseBasic(t *testing.T) {
	_, stop, cli, serverSessionCh, err := setupHalfCloseServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// Create client session
	clientSession, _ := Client(cli, nil)
	defer clientSession.Close()

	// Get server session
	serverSession := <-serverSessionCh
	defer serverSession.Close()

	// Open stream from client
	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}

	// Accept stream on server
	serverStream, err := serverSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	// Client writes data then half-closes
	testData := []byte("hello from client")
	_, err = clientStream.Write(testData)
	if err != nil {
		t.Fatal("client write failed:", err)
	}

	// Client closes write side (half-close)
	err = clientStream.CloseWrite()
	if err != nil {
		t.Fatal("CloseWrite failed:", err)
	}

	// Server should be able to read the data
	buf := make([]byte, len(testData))
	n, err := io.ReadFull(serverStream, buf)
	if err != nil {
		t.Fatal("server read failed:", err)
	}
	if !bytes.Equal(buf[:n], testData) {
		t.Fatalf("data mismatch: got %v, want %v", buf[:n], testData)
	}

	// Server should get EOF on next read (because client sent FIN)
	_, err = serverStream.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after CloseWrite, got %v", err)
	}

	// Server should still be able to write back
	responseData := []byte("response from server")
	_, err = serverStream.Write(responseData)
	if err != nil {
		t.Fatal("server write failed after client CloseWrite:", err)
	}

	// Client should be able to read the response
	responseBuf := make([]byte, len(responseData))
	n, err = io.ReadFull(clientStream, responseBuf)
	if err != nil {
		t.Fatal("client read failed:", err)
	}
	if !bytes.Equal(responseBuf[:n], responseData) {
		t.Fatalf("response mismatch: got %v, want %v", responseBuf[:n], responseData)
	}

	// Client write should fail after CloseWrite
	_, err = clientStream.Write([]byte("should fail"))
	if err != io.ErrClosedPipe {
		t.Fatalf("write after CloseWrite should return io.ErrClosedPipe, got %v", err)
	}
}

// TestHalfCloseDoubleCloseWrite tests that calling CloseWrite twice returns error
func TestHalfCloseDoubleCloseWrite(t *testing.T) {
	_, stop, cli, serverSessionCh, err := setupHalfCloseServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	clientSession, _ := Client(cli, nil)
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, _ := clientSession.OpenStream()
	serverSession.AcceptStream() // accept to avoid blocking

	// First CloseWrite should succeed
	err = clientStream.CloseWrite()
	if err != nil {
		t.Fatal("first CloseWrite failed:", err)
	}

	// Second CloseWrite should return error
	err = clientStream.CloseWrite()
	if err != io.ErrClosedPipe {
		t.Fatalf("second CloseWrite should return io.ErrClosedPipe, got %v", err)
	}
}

// TestHalfCloseBidirectional tests both sides doing half-close
func TestHalfCloseBidirectional(t *testing.T) {
	_, stop, cli, serverSessionCh, err := setupHalfCloseServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	clientSession, _ := Client(cli, nil)
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, _ := clientSession.OpenStream()
	serverStream, _ := serverSession.AcceptStream()

	var wg sync.WaitGroup
	wg.Add(2)

	// Client writes, half-closes, then reads
	go func() {
		defer wg.Done()
		clientStream.Write([]byte("client data"))
		clientStream.CloseWrite()

		buf := make([]byte, 100)
		n, err := clientStream.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("client read failed: %v", err)
			return
		}
		if string(buf[:n]) != "server data" {
			t.Errorf("client got wrong data: %s", buf[:n])
		}
	}()

	// Server writes, half-closes, then reads
	go func() {
		defer wg.Done()
		serverStream.Write([]byte("server data"))
		serverStream.CloseWrite()

		buf := make([]byte, 100)
		n, err := serverStream.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("server read failed: %v", err)
			return
		}
		if string(buf[:n]) != "client data" {
			t.Errorf("server got wrong data: %s", buf[:n])
		}
	}()

	wg.Wait()
}

// TestHalfCloseWithFullClose tests that Close() still works correctly
func TestHalfCloseWithFullClose(t *testing.T) {
	_, stop, cli, serverSessionCh, err := setupHalfCloseServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	clientSession, _ := Client(cli, nil)
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, _ := clientSession.OpenStream()
	serverStream, _ := serverSession.AcceptStream()

	// Write some data
	clientStream.Write([]byte("hello"))

	// Full close should still work
	err = clientStream.Close()
	if err != nil {
		t.Fatal("Close failed:", err)
	}

	// Double close should return error
	err = clientStream.Close()
	if err != io.ErrClosedPipe {
		t.Fatalf("double Close should return io.ErrClosedPipe, got %v", err)
	}

	// Server should get EOF
	buf := make([]byte, 100)
	serverStream.SetReadDeadline(time.Now().Add(time.Second))
	for {
		_, err = serverStream.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("unexpected error:", err)
		}
	}
}

// TestHalfCloseV2 tests half-close with protocol version 2
func TestHalfCloseV2(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	config := DefaultConfig()
	config.Version = 2

	serverSessionCh := make(chan *Session, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sess, _ := Server(conn, config)
		serverSessionCh <- sess
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	clientSession, _ := Client(conn, config)
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, _ := clientSession.OpenStream()
	serverStream, _ := serverSession.AcceptStream()

	// Client writes data then half-closes
	testData := []byte("hello v2")
	clientStream.Write(testData)
	clientStream.CloseWrite()

	// Server reads data
	buf := make([]byte, len(testData))
	io.ReadFull(serverStream, buf)
	if !bytes.Equal(buf, testData) {
		t.Fatalf("data mismatch: got %v, want %v", buf, testData)
	}

	// Server should get EOF
	_, err = serverStream.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	// Server can still write
	responseData := []byte("response v2")
	_, err = serverStream.Write(responseData)
	if err != nil {
		t.Fatal("server write failed:", err)
	}

	// Client can still read
	responseBuf := make([]byte, len(responseData))
	_, err = io.ReadFull(clientStream, responseBuf)
	if err != nil {
		t.Fatal("client read failed:", err)
	}
	if !bytes.Equal(responseBuf, responseData) {
		t.Fatalf("response mismatch: got %v, want %v", responseBuf, responseData)
	}
}
