package smux

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync/atomic"
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

func TestBufferRingShrinksWhenDrained(t *testing.T) {
	for _, mode := range []string{"pop", "consume"} {
		t.Run(mode, func(t *testing.T) {
			r := newBufferRing(defaultBufferRingCapacity)
			for i := range defaultBufferRingCapacity * 2 {
				buf := []byte{byte(i)}
				r.push(buf, nil)
			}
			if len(r.bufs) <= defaultBufferRingCapacity {
				t.Fatal("expected ring to grow")
			}

			for r.len() > 0 {
				switch mode {
				case "pop":
					if _, _, ok := r.pop(); !ok {
						t.Fatal("unexpected empty ring")
					}
				case "consume":
					if n, _ := r.consumeFront(make([]byte, 1)); n != 1 {
						t.Fatalf("consumed %d bytes; want 1", n)
					}
				}
			}
			if got := len(r.bufs); got != defaultBufferRingCapacity {
				t.Fatalf("ring capacity = %d; want %d", got, defaultBufferRingCapacity)
			}
		})
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
	clientSession, err := Client(cli, nil)
	if err != nil {
		t.Fatal(err)
	}
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

	clientSession, err := Client(cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverSession.AcceptStream(); err != nil {
		t.Fatal(err)
	}

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

	clientSession, err := Client(cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	serverStream, err := serverSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	if err := clientStream.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := serverStream.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}

	clientData := []byte("client data")
	serverData := []byte("server data")
	if n, err := clientStream.Write(clientData); err != nil || n != len(clientData) {
		t.Fatalf("client write = (%d, %v); want (%d, nil)", n, err, len(clientData))
	}
	if n, err := serverStream.Write(serverData); err != nil || n != len(serverData) {
		t.Fatalf("server write = (%d, %v); want (%d, nil)", n, err, len(serverData))
	}
	if err := clientStream.CloseWrite(); err != nil {
		t.Fatal("client CloseWrite failed:", err)
	}
	if err := serverStream.CloseWrite(); err != nil {
		t.Fatal("server CloseWrite failed:", err)
	}

	clientReceived := make([]byte, len(serverData))
	if _, err := io.ReadFull(clientStream, clientReceived); err != nil {
		t.Fatal("client read failed:", err)
	}
	if !bytes.Equal(clientReceived, serverData) {
		t.Fatalf("client received %q; want %q", clientReceived, serverData)
	}
	serverReceived := make([]byte, len(clientData))
	if _, err := io.ReadFull(serverStream, serverReceived); err != nil {
		t.Fatal("server read failed:", err)
	}
	if !bytes.Equal(serverReceived, clientData) {
		t.Fatalf("server received %q; want %q", serverReceived, clientData)
	}
	if _, err := clientStream.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("client read after peer half-close = %v; want EOF", err)
	}
	if _, err := serverStream.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("server read after peer half-close = %v; want EOF", err)
	}
}

// TestHalfCloseWithFullClose tests that Close() still works correctly
func TestHalfCloseWithFullClose(t *testing.T) {
	_, stop, cli, serverSessionCh, err := setupHalfCloseServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	clientSession, err := Client(cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	serverSession := <-serverSessionCh
	defer serverSession.Close()

	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	serverStream, err := serverSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	// Write some data
	payload := []byte("hello")
	if n, err := clientStream.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("client write = (%d, %v); want (%d, nil)", n, err, len(payload))
	}

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
	if err := serverStream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var received bytes.Buffer
	for {
		n, err := serverStream.Read(buf)
		received.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("unexpected error:", err)
		}
	}
	if !bytes.Equal(received.Bytes(), payload) {
		t.Fatalf("server received %q; want %q", received.Bytes(), payload)
	}
}

func TestHalfCloseAutoCleanup(t *testing.T) {
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

	if err := clientStream.CloseWrite(); err != nil {
		t.Fatal("client CloseWrite failed:", err)
	}
	if err := serverStream.CloseWrite(); err != nil {
		t.Fatal("server CloseWrite failed:", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if clientSession.NumStreams() == 0 && serverSession.NumStreams() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("streams not cleaned up: client=%d server=%d", clientSession.NumStreams(), serverSession.NumStreams())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHalfClosePreservesUnreadData(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, localFirst := range []bool{false, true} {
			for _, useWriteTo := range []bool{false, true} {
				for _, closeUnread := range []bool{false, true} {
					t.Run(fmt.Sprintf("v%d/localFirst=%t/writeTo=%t/closeUnread=%t", version, localFirst, useWriteTo, closeUnread), func(t *testing.T) {
						ln, err := net.Listen("tcp", "localhost:0")
						if err != nil {
							t.Fatal(err)
						}
						defer ln.Close()

						config := DefaultConfig()
						config.Version = version
						serverSessionCh := make(chan *Session, 1)
						serverErrCh := make(chan error, 1)
						go func() {
							conn, err := ln.Accept()
							if err != nil {
								serverErrCh <- err
								return
							}
							sess, err := Server(conn, config)
							if err != nil {
								serverErrCh <- err
								return
							}
							serverSessionCh <- sess
						}()

						conn, err := net.Dial("tcp", ln.Addr().String())
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()

						clientSession, err := Client(conn, config)
						if err != nil {
							t.Fatal(err)
						}
						defer clientSession.Close()

						var serverSession *Session
						select {
						case serverSession = <-serverSessionCh:
						case err := <-serverErrCh:
							t.Fatal(err)
						case <-time.After(5 * time.Second):
							t.Fatal("timed out waiting for server session")
						}
						defer serverSession.Close()

						clientStream, err := clientSession.OpenStream()
						if err != nil {
							t.Fatal(err)
						}
						serverStream, err := serverSession.AcceptStream()
						if err != nil {
							t.Fatal(err)
						}

						payload := []byte("buffered response survives half-close")
						if localFirst {
							if err := serverStream.CloseWrite(); err != nil {
								t.Fatal("server CloseWrite failed:", err)
							}
						}
						if _, err := clientStream.Write(payload); err != nil {
							t.Fatal("client write failed:", err)
						}
						if err := clientStream.CloseWrite(); err != nil {
							t.Fatal("client CloseWrite failed:", err)
						}

						if !localFirst {
							select {
							case <-serverStream.chFinEvent:
							case <-time.After(5 * time.Second):
								t.Fatal("timed out waiting for peer FIN")
							}
							if err := serverStream.CloseWrite(); err != nil {
								t.Fatal("server CloseWrite failed:", err)
							}
						}

						if closeUnread {
							select {
							case <-serverStream.chFinEvent:
							case <-time.After(5 * time.Second):
								t.Fatal("timed out waiting for peer FIN")
							}
							select {
							case <-serverStream.die:
							case <-time.After(5 * time.Second):
								t.Fatal("timed out waiting for half-close cleanup")
							}

							serverStream.bufferLock.Lock()
							buffered := serverStream.bufferRing.len()
							serverStream.bufferLock.Unlock()
							if buffered == 0 {
								t.Fatal("expected unread data before explicit Close")
							}

							if err := serverStream.Close(); err != io.ErrClosedPipe {
								t.Fatalf("Close after half-close = %v; want %v", err, io.ErrClosedPipe)
							}

							serverStream.bufferLock.Lock()
							buffered = serverStream.bufferRing.len()
							serverStream.bufferLock.Unlock()
							if buffered != 0 {
								t.Fatalf("Close left %d unread buffers", buffered)
							}
							if serverSession.NumStreams() != 0 {
								t.Fatalf("Close left %d streams in server session", serverSession.NumStreams())
							}
							if got := atomic.LoadInt32(&serverSession.bucket); got != int32(config.MaxReceiveBuffer) {
								t.Fatalf("Close left receive bucket at %d; want %d", got, config.MaxReceiveBuffer)
							}
							return
						}

						if err := serverStream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
							t.Fatal(err)
						}
						var got bytes.Buffer
						if useWriteTo {
							if _, err := io.Copy(&got, serverStream); err != nil {
								t.Fatal("io.Copy failed:", err)
							}
						} else {
							buf := make([]byte, 3)
							for {
								n, err := serverStream.Read(buf)
								got.Write(buf[:n])
								if err == io.EOF {
									break
								}
								if err != nil {
									t.Fatal("server read failed:", err)
								}
							}
						}
						if !bytes.Equal(got.Bytes(), payload) {
							t.Fatalf("received %q; want %q", got.Bytes(), payload)
						}

						deadline := time.Now().Add(5 * time.Second)
						for serverSession.NumStreams() != 0 || clientSession.NumStreams() != 0 {
							if time.Now().After(deadline) {
								t.Fatalf("streams not cleaned up: client=%d server=%d", clientSession.NumStreams(), serverSession.NumStreams())
							}
							time.Sleep(time.Millisecond)
						}
					})
				}
			}
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
	serverErrCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErrCh <- err
			return
		}
		sess, err := Server(conn, config)
		if err != nil {
			serverErrCh <- err
			return
		}
		serverSessionCh <- sess
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	clientSession, err := Client(conn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	var serverSession *Session
	select {
	case serverSession = <-serverSessionCh:
	case err := <-serverErrCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server session")
	}
	defer serverSession.Close()

	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	serverStream, err := serverSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	if err := clientStream.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := serverStream.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}

	// Client writes data then half-closes
	testData := []byte("hello v2")
	if n, err := clientStream.Write(testData); err != nil || n != len(testData) {
		t.Fatalf("client write = (%d, %v); want (%d, nil)", n, err, len(testData))
	}
	if err := clientStream.CloseWrite(); err != nil {
		t.Fatal("client CloseWrite failed:", err)
	}

	// Server reads data
	buf := make([]byte, len(testData))
	if _, err := io.ReadFull(serverStream, buf); err != nil {
		t.Fatal("server read failed:", err)
	}
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
	if n, err := serverStream.Write(responseData); err != nil || n != len(responseData) {
		t.Fatalf("server write = (%d, %v); want (%d, nil)", n, err, len(responseData))
	}
	if err := serverStream.CloseWrite(); err != nil {
		t.Fatal("server CloseWrite failed:", err)
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

func TestWriteToV2WindowUpdateAfterDestinationError(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	config := DefaultConfig()
	config.Version = 2
	serverSessionCh := make(chan *Session, 1)
	serverErrCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErrCh <- err
			return
		}
		sess, err := Server(conn, config)
		if err != nil {
			serverErrCh <- err
			return
		}
		serverSessionCh <- sess
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	clientSession, err := Client(conn, config)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	var serverSession *Session
	select {
	case serverSession = <-serverSessionCh:
	case err := <-serverErrCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server session")
	}
	defer serverSession.Close()

	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	serverStream, err := serverSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("data consumed despite destination short write")
	if _, err := clientStream.Write(payload); err != nil {
		t.Fatal("client write failed:", err)
	}

	if n, err := serverStream.WriteTo(shortWriteWriter{n: 0}); n != 0 || err != io.ErrShortWrite {
		t.Fatalf("WriteTo = (%d, %v); want (0, %v)", n, err, io.ErrShortWrite)
	}

	deadline := time.Now().Add(5 * time.Second)
	for clientStream.peerConsumed.Load() < uint32(len(payload)) {
		if time.Now().After(deadline) {
			t.Fatalf("peer consumed = %d; want at least %d", clientStream.peerConsumed.Load(), len(payload))
		}
		time.Sleep(time.Millisecond)
	}
}
