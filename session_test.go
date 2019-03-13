package smux

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	//"runtime"
	"runtime/pprof"
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func init() {
	go func() {
		http.ListenAndServe("localhost:6060", nil)
	}()
}

// setupServer starts new server listening on a random localhost port and
// returns address of the server, function to stop the server, new client
// connection to this server or an error.
func setupServer(tb testing.TB) (addr string, stopfunc func(), client net.Conn, err error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, nil, err
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			tb.Error(err)
			return
		}
		go handleConnection(conn)
	}()
	time.Sleep(20 * time.Millisecond)
	addr = ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		return "", nil, nil, err
	}
	return ln.Addr().String(), func() { ln.Close() }, conn, nil
}

func setupServerPipe(tb testing.TB) (addr string, stopfunc func(), client net.Conn, err error) {
	ln, conn := net.Pipe()
	go handleConnection(ln)
	return "", func() { ln.Close() }, conn, nil
}

func handleConnection(conn net.Conn) {
	session, _ := Server(conn, nil)
	for {
		if stream, err := session.AcceptStream(); err == nil {
			go func(s io.ReadWriteCloser) {
				buf := make([]byte, 65536)
				for {
					n, err := s.Read(buf)
					if err != nil {
						return
					}
					s.Write(buf[:n])
				}
			}(stream)
		} else {
			return
		}
	}
}

func TestEcho(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	buf := make([]byte, 10)
	var sent string
	var received string
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		stream.Write([]byte(msg))
		sent += msg
		if n, err := stream.Read(buf); err != nil {
			t.Fatal(err)
		} else {
			received += string(buf[:n])
		}
	}
	if sent != received {
		t.Fatal("data mimatch")
	}
	session.Close()
}

func TestGetDieCh(t *testing.T) {
	cs, ss, err := getSmuxStreamPair(nil)
	if err != nil {
		t.Fatal(err)
	}
	testGetDieCh(t, cs, ss)
}

func TestGetDieCh2(t *testing.T) {
	cs, ss, _ := getSmuxStreamPair(nil)
	testGetDieCh(t, cs, ss)
}

func testGetDieCh(t *testing.T, cs, ss *Stream) {
	defer ss.Close()
	dieCh := ss.GetDieCh()
	go func() {
		select {
		case <-dieCh:
		case <-time.Tick(time.Second):
			t.Fatal("wait die chan timeout")
		}
	}()
	cs.Close()
}

func TestSpeed(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	testSpeed(t, cli)
}

func TestSpeed2(t *testing.T) {
	_, stop, cli, _ := setupServerPipe(t)
	defer stop()
	testSpeed(t, cli)
}

func testSpeed(t *testing.T, cli net.Conn) {
	defer cli.Close()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	t.Log(stream.LocalAddr(), stream.RemoteAddr())

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buf := make([]byte, 1024*1024)
		nrecv := 0
		for {
			n, err := stream.Read(buf)
			if err != nil {
				t.Error(err)
				break
			} else {
				nrecv += n
				if nrecv == 4096*4096 {
					break
				}
			}
		}
		stream.Close()
		t.Log("time for 16MB rtt", time.Since(start))
		wg.Done()
	}()
	msg := make([]byte, 8192)
	for i := 0; i < 2048; i++ {
		stream.Write(msg)
	}
	wg.Wait()
	session.Close()
}

func TestParallel(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	testParallel(t, cli)
}

func TestParallel2(t *testing.T) {
	_, stop, cli, _ := setupServerPipe(t)
	defer stop()
	testParallel(t, cli)
}

func testParallel(t *testing.T, cli net.Conn) {
	defer cli.Close()
	session, _ := Client(cli, nil)

	par := 1000
	messages := 100
	die := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < par; i++ {
		stream, err := session.OpenStream()
		if err != nil {
			dumpGoroutine(t)
			t.Fatalf("cannot create stream %v: %v", i, err)
			break
		}
		wg.Add(1)
		go func(s *Stream) {
			buf := make([]byte, 20)

			for { // keep read & write untill all stream end
				select {
				case <-die:
					goto END
				default:
				}
				for j := 0; j < messages; j++ {
					msg := fmt.Sprintf("hello%v", j)
					s.Write([]byte(msg))
					if _, err := s.Read(buf); err != nil {
						break
					}
				}
			}
		END:
			//<-die
			s.Close()
			wg.Done()
		}(stream)
	}
	t.Log("created", session.NumStreams(), "streams and keep streams do read & write")
	time.Sleep(500 * time.Millisecond)
	t.Log("kill all", session.NumStreams(), "streams")
	close(die)
	wg.Wait()
	t.Log("all", session.NumStreams(), "streams end")
	session.Close()
}

func TestCloseThenOpen(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	session.Close()
	if _, err := session.OpenStream(); err == nil {
		t.Fatal("opened after close")
	}
}

func TestStreamDoubleClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	stream.Close()
	if err := stream.Close(); err == nil {
		t.Log("double close doesn't return error")
	}
	session.Close()
}

func TestConcurrentClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	numStreams := 1000
	streams := make([]*Stream, 0, numStreams)
	for i := 0; i < numStreams; i++ {
		stream, _ := session.OpenStream()
		streams = append(streams, stream)
	}
	var wg sync.WaitGroup
	for _, s := range streams {
		wg.Add(1)
		go func(stream *Stream) {
			stream.Close()
			wg.Done()
		}(s)
	}
	session.Close()
	wg.Wait()
}

func TestTinyReadBuffer(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	testTinyReadBuffer(t, cli)
}

func TestTinyReadBuffer2(t *testing.T) {
	_, stop, cli, _ := setupServerPipe(t)
	defer stop()
	testTinyReadBuffer(t, cli)
}

func TestTinyReadBuffer3(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	go handleConnection(srv)
	testTinyReadBuffer(t, cli)
}

func testTinyReadBuffer(t *testing.T, cli net.Conn) {
	defer cli.Close()

	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	tinybuf := make([]byte, 6)
	var sent string
	var received string
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		sent += msg
		nsent, err := stream.Write([]byte(msg))
		if err != nil {
			t.Fatal("cannot write")
		}
		nrecv := 0
		for nrecv < nsent {
			if n, err := stream.Read(tinybuf); err == nil {
				nrecv += n
				received += string(tinybuf[:n])
			} else {
				t.Fatal("cannot read with tiny buffer")
			}
		}
	}

	if sent != received {
		t.Fatal("data mimatch")
	}
	session.Close()
}

func TestIsClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	session.Close()
	if !session.IsClosed() {
		t.Fatal("still open after close")
	}
}

func TestKeepAliveTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		ln.Accept()
	}()
	defer ln.Close()

	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	testKeepAliveTimeout(t, cli)
}

func TestKeepAliveTimeout2(t *testing.T) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	defer c2.Close()
	testKeepAliveTimeout(t, c1)
}

func TestKeepAliveTimeout3(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	testKeepAliveTimeout(t, c1)
}

func testKeepAliveTimeout(t *testing.T, cli net.Conn) {
	config := DefaultConfig()
	config.KeepAliveInterval = time.Second
	config.KeepAliveTimeout = 2 * time.Second
	session, _ := Client(cli, config)
	time.Sleep(3 * time.Second)
	if !session.IsClosed() {
		t.Fatal("keepalive-timeout failed")
	}
}

type delayWriteConn struct {
	net.Conn
	Delay time.Duration
}

func (c *delayWriteConn) Write(b []byte) (n int, err error) {
	time.Sleep(c.Delay)
	return c.Conn.Write(b)
}

func TestKeepAliveBlockWriteTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		ln.Accept()
	}()

	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	testKeepAliveBlockWriteTimeout(t, cli)
}

func TestKeepAliveBlockWriteTimeout2(t *testing.T) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	defer c2.Close()
	testKeepAliveBlockWriteTimeout(t, c1)
}

func TestKeepAliveBlockWriteTimeout3(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	testKeepAliveBlockWriteTimeout(t, c1)
}

func testKeepAliveBlockWriteTimeout(t *testing.T, cli net.Conn) {
	//when writeFrame block, keepalive in old version never timeout
	blockWriteCli := &delayWriteConn{cli, 24 * time.Hour}

	config := DefaultConfig()
	config.KeepAliveInterval = time.Second
	config.KeepAliveTimeout = 2 * time.Second
	session, _ := Client(blockWriteCli, config)
	time.Sleep(3 * time.Second)
	if !session.IsClosed() {
		t.Fatal("keepalive-timeout failed")
	}
}

func TestKeepAliveDelayWriteTimeout(t *testing.T) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
	}
	testKeepAliveDelayWriteTimeout(t, c1, c2)
}

func TestKeepAliveDelayWriteTimeoutPipe(t *testing.T) {
	c1, c2 := net.Pipe()
	testKeepAliveDelayWriteTimeout(t, c1, c2)
}

func testKeepAliveDelayWriteTimeout(t *testing.T, c1 net.Conn, c2 net.Conn) {
	defer c1.Close()
	defer c2.Close()

	configSrv := DefaultConfig()
	configSrv.KeepAliveInterval = 23 * time.Hour // never send ping
	configSrv.KeepAliveTimeout = 24 * time.Hour // never check
	srv, _ := Server(c2, configSrv)
	defer srv.Close()

	// delay 200 ms, old KeepAlive will timeout
	delayWriteCli := &delayWriteConn{c1, 200 * time.Millisecond}
	//delayWriteCli := &delayWriteConn{c1, 24 * time.Hour}

	config := DefaultConfig()
	config.KeepAliveInterval = 200 * time.Millisecond // send @ 200ms
	config.KeepAliveTimeout = 300 * time.Millisecond // should check after 300 ms (= 500 ms), not @ 300 ms
	session, _ := Client(delayWriteCli, config)
	time.Sleep(2 * time.Second)
	if session.IsClosed() {
		t.Fatal("keepalive-timeout failed, close too quickly")
	}
}

func TestServerEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		err := func() error {
			conn, err := ln.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			session, err := Server(conn, nil)
			if err != nil {
				return err
			}
			defer session.Close()
			buf := make([]byte, 10)
			stream, err := session.OpenStream()
			if err != nil {
				return err
			}
			defer stream.Close()
			for i := 0; i < 100; i++ {
				msg := fmt.Sprintf("hello%v", i)
				stream.Write([]byte(msg))
				n, err := stream.Read(buf)
				if err != nil {
					return err
				}
				if got := string(buf[:n]); got != msg {
					return fmt.Errorf("got: %q, want: %q", got, msg)
				}
			}
			return nil
		}()
		if err != nil {
			t.Error(err)
		}
	}()

	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if session, err := Client(cli, nil); err == nil {
		if stream, err := session.AcceptStream(); err == nil {
			buf := make([]byte, 65536)
			for {
				n, err := stream.Read(buf)
				if err != nil {
					break
				}
				stream.Write(buf[:n])
			}
		} else {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
}

func TestServerEcho2(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()

	go func() {
		 session, _ := Server(srv, nil)
		 if stream, err := session.OpenStream(); err == nil {
			  const N = 100
			  buf := make([]byte, 10)
			  for i := 0; i < N; i++ {
				   msg := fmt.Sprintf("hello%v", i)
				   stream.Write([]byte(msg))
				   if n, err := stream.Read(buf); err != nil {
					    t.Fatal(err)
				   } else if string(buf[:n]) != msg {
					    t.Fatal(err)
				   }
			  }
			  stream.Close()
		 } else {
			  t.Fatal(err)
		 }
	}()

	if session, err := Client(cli, nil); err == nil {
		 if stream, err := session.AcceptStream(); err == nil {
			  buf := make([]byte, 65536)
			  for {
				   n, err := stream.Read(buf)
				   if err != nil {
					    break
				   }
				   stream.Write(buf[:n])
			  }
		 } else {
			  t.Fatal(err)
		 }
	} else {
		 t.Fatal(err)
	}
}


func TestSendWithoutRecv(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		stream.Write([]byte(msg))
	}
	buf := make([]byte, 1)
	if _, err := stream.Read(buf); err != nil {
		t.Fatal(err)
	}
	stream.Close()
}

func TestWriteAfterClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	stream.Close()
	if _, err := stream.Write([]byte("write after close")); err == nil {
		t.Fatal("write after close failed")
	}
}

func TestReadStreamAfterSessionClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	session.Close()
	buf := make([]byte, 10)
	if _, err := stream.Read(buf); err != nil {
		t.Log(err)
	} else {
		t.Fatal("read stream after session close succeeded")
	}
}

func TestWriteStreamAfterConnectionClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	session.conn.Close()
	if _, err := stream.Write([]byte("write after connection close")); err == nil {
		t.Fatal("write after connection close failed")
	}
}

func TestNumStreamAfterClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	if _, err := session.OpenStream(); err == nil {
		if session.NumStreams() != 1 {
			t.Fatal("wrong number of streams after opened")
		}
		session.Close()
		if session.NumStreams() != 0 {
			t.Fatal("wrong number of streams after session closed")
		}
	} else {
		t.Fatal(err)
	}
	cli.Close()
}

func TestRandomFrame(t *testing.T) {
	addr, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// pure random
	session, _ := Client(cli, nil)
	for i := 0; i < 100; i++ {
		rnd := make([]byte, rand.Uint32()%1024)
		io.ReadFull(crand.Reader, rnd)
		session.conn.Write(rnd)
	}
	cli.Close()

	// double syn
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(cmdSYN, 1000)
		session.writeFrame(f)
	}
	cli.Close()

	// random cmds
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	allcmds := []byte{cmdSYN, cmdFIN, cmdPSH, cmdNOP, cmdACK, cmdFUL, cmdEMP}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(allcmds[rand.Int()%len(allcmds)], rand.Uint32())
		session.writeFrame(f)
	}
	cli.Close()

	// random cmds & sids
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(byte(rand.Uint32()), rand.Uint32())
		session.writeFrame(f)
	}
	cli.Close()

	// random version
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(byte(rand.Uint32()), rand.Uint32())
		f.ver = byte(rand.Uint32())
		session.writeFrame(f)
	}
	cli.Close()

	// incorrect size
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)

	f := newFrame(byte(rand.Uint32()), rand.Uint32())
	rnd := make([]byte, rand.Uint32()%1024)
	io.ReadFull(crand.Reader, rnd)
	f.data = rnd

	buf := make([]byte, headerSize+len(f.data))
	buf[0] = f.ver
	buf[1] = f.cmd
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(rnd)+1)) /// incorrect size
	binary.LittleEndian.PutUint32(buf[4:], f.sid)
	copy(buf[headerSize:], f.data)

	session.conn.Write(buf)
	cli.Close()

	// writeFrame after die
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	//close first
	session.Close()
	for i := 0; i < 100; i++ {
		f := newFrame(byte(rand.Uint32()), rand.Uint32())
		session.writeFrame(f)
	}
}

func TestWriteFrameInternal(t *testing.T) {
	addr, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// pure random
	session, _ := Client(cli, nil)
	for i := 0; i < 100; i++ {
		rnd := make([]byte, rand.Uint32()%1024)
		io.ReadFull(crand.Reader, rnd)
		session.conn.Write(rnd)
	}
	cli.Close()

	// writeFrame after die
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	//close first
	session.Close()
	for i := 0; i < 100; i++ {
		f := newFrame(byte(rand.Uint32()), rand.Uint32())
		session.writeFrameInternal(f, time.After(session.config.KeepAliveTimeout))
	}

	// random cmds
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	allcmds := []byte{cmdSYN, cmdFIN, cmdPSH, cmdNOP}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(allcmds[rand.Int()%len(allcmds)], rand.Uint32())
		session.writeFrameInternal(f, time.After(session.config.KeepAliveTimeout))
	}
	//deadline occur
	{
		c := make(chan time.Time)
		close(c)
		f := newFrame(allcmds[rand.Int()%len(allcmds)], rand.Uint32())
		_, err := session.writeFrameInternal(f, c)
		if err != errTimeout {
			t.Fatal("write frame with deadline failed", err)
		}
	}
	cli.Close()

	{
		cli, err = net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		config := DefaultConfig()
		config.KeepAliveInterval = time.Second
		config.KeepAliveTimeout = 2 * time.Second
		session, _ = Client(&delayWriteConn{cli, 24 * time.Hour}, config)
		f := newFrame(byte(rand.Uint32()), rand.Uint32())
		c := make(chan time.Time)
		go func() {
			//die first, deadline second, better for coverage
			time.Sleep(time.Second)
			session.Close()
			time.Sleep(time.Second)
			close(c)
		}()
		_, err = session.writeFrameInternal(f, c)
		if err.Error() != ErrBrokenPipe.Error() {
			t.Fatal("write frame with deadline failed", err)
		}
	}
}

func TestReadDeadline(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	testReadDeadline(t, cli)
}

func TestReadDeadline2(t *testing.T) {
	_, stop, cli, _ := setupServerPipe(t)
	defer stop()
	testReadDeadline(t, cli)
}

func testReadDeadline(t *testing.T, cli net.Conn) {
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	buf := make([]byte, 10)
	var readErr error
	for i := 0; i < N; i++ {
		stream.SetReadDeadline(time.Now().Add(-1 * time.Minute))
		if _, readErr = stream.Read(buf); readErr != nil {
			break
		}
	}
	if readErr != nil {
		if !strings.Contains(readErr.Error(), "i/o timeout") {
			t.Fatalf("Wrong error: %v", readErr)
		}
	} else {
		t.Fatal("No error when reading with past deadline")
	}
	session.Close()
}

func TestWriteDeadline(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	testWriteDeadline(t, cli)
}

func TestWriteDeadline2(t *testing.T) {
	_, stop, cli, _ := setupServerPipe(t)
	defer stop()
	testWriteDeadline(t, cli)
}

func testWriteDeadline(t *testing.T, cli net.Conn) {
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	buf := make([]byte, 10)
	var writeErr error
	for {
		stream.SetWriteDeadline(time.Now().Add(-1 * time.Minute))
		if _, writeErr = stream.Write(buf); writeErr != nil {
			if !strings.Contains(writeErr.Error(), "i/o timeout") {
				t.Fatalf("Wrong error: %v", writeErr)
			}
			break
		}
	}
	session.Close()
}

func TestSlowReadBlocking(t *testing.T) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
		return
	}

	testSlowReadBlocking(t, c1, c2)
}

func TestSlowReadBlocking2(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()

	testSlowReadBlocking(t, srv, cli)
}

func testSlowReadBlocking(t *testing.T, srv net.Conn, cli net.Conn) {
	config := &Config{
		KeepAliveInterval:  100 * time.Millisecond,
		KeepAliveTimeout:   500 * time.Millisecond,
		MaxFrameSize:       4096,
		MaxReceiveBuffer:   1 * 1024 * 1024,
		EnableStreamBuffer: true,
		MaxStreamBuffer:    8192,
		BoostTimeout:       0 * time.Millisecond,
	}

	go func (conn net.Conn) {
		session, _ := Server(conn, config)
		for {
			if stream, err := session.AcceptStream(); err == nil {
				go func(s io.ReadWriteCloser) {
					buf := make([]byte, 1024 * 1024, 1024 * 1024)
					for {
						n, err := s.Read(buf)
						if err != nil {
							return
						}
						//t.Log("s1", stream.id, "session.bucket", atomic.LoadInt32(&session.bucket), "stream.bucket", atomic.LoadInt32(&stream.bucket), n)
						s.Write(buf[:n])
					}
				}(stream)
			} else {
				return
			}
		}
	}(srv)

	session, _ := Client(cli, config)
	startNotify := make(chan bool, 1)
	flag := int32(1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { // fast write & slow read
		defer wg.Done()

		stream, err := session.OpenStream()
		if err == nil {
			t.Log("fast write stream start...")
			defer func() {
				stream.Close()
				t.Log("fast write stream end...")
			}()

			const SIZE = 1 * 1024 // Bytes
			const SPDW = 16 * 1024 * 1024 // Bytes/s
			const SPDR = 512 * 1024 // Bytes/s
			const TestDtW = time.Second / time.Duration(SPDW/SIZE)
			const TestDtR = time.Second / time.Duration(SPDR/SIZE)

			var fwg sync.WaitGroup
			fwg.Add(1)
			go func() { // read = SPDR
				defer fwg.Done()
				rbuf := make([]byte, SIZE, SIZE)
				for atomic.LoadInt32(&flag) > 0 {
					stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					if _, err := stream.Read(rbuf); err != nil {
						if strings.Contains(err.Error(), "i/o timeout") {
							//t.Logf("read block too long: %v", err)
							continue
						}
						break
					}
					time.Sleep(TestDtR) // slow down read
				}
			}()

			buf := make([]byte, SIZE, SIZE)
			for i := range buf {
				buf[i] = byte('-')
			}
			startNotify <- true
			for atomic.LoadInt32(&flag) > 0 { // write = SPDW
				stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_, err := stream.Write(buf)
				if err != nil {
					if strings.Contains(err.Error(), "i/o timeout") {
						//t.Logf("write block too long: %v", err)
						continue
					}
					break
				}
				//t.Log("f2", stream.id, "session.bucket", atomic.LoadInt32(&session.bucket), "stream.bucket", atomic.LoadInt32(&stream.bucket))
				time.Sleep(TestDtW) // slow down write
			}
			fwg.Wait()

		} else {
			t.Fatal(err)
		}
	}()

	wg.Add(1)
	go func() { // normal write, rtt test
		defer func() {
			session.Close()
			wg.Done()
		}()

		stream, err := session.OpenStream()
		if err == nil {
			t.Log("normal stream start...")
			defer func() {
				atomic.StoreInt32(&flag, int32(0))
				stream.Close()
				t.Log("normal stream end...")
			}()

			const N = 100
			const TestDt = 50 * time.Millisecond
			const TestTimeout = 500 * time.Millisecond

			buf := make([]byte, 12)
			<- startNotify
			for i := 0; i < N; i++ {
				msg := fmt.Sprintf("hello%v", i)
				start := time.Now()

				stream.SetWriteDeadline(time.Now().Add(TestTimeout))
				_, err := stream.Write([]byte(msg))
				if err != nil && strings.Contains(err.Error(), "i/o timeout") {
					t.Log(stream.id, i, err,
						"session.bucket", atomic.LoadInt32(&session.bucket),
						"stream.bucket", atomic.LoadInt32(&stream.bucket),
						"stream.empflag", atomic.LoadInt32(&stream.empflag), "stream.fulflag", atomic.LoadInt32(&stream.fulflag))
					dumpGoroutine(t)
					t.Fatal(err)
					return
				}

				/*t.Log("[normal]w", stream.id, i, "rtt", time.Since(start),
					"stream.bucket", atomic.LoadInt32(&stream.bucket),
					"stream.guessNeeded", atomic.LoadInt32(&stream.guessNeeded))*/

				stream.SetReadDeadline(time.Now().Add(TestTimeout))
				if n, err := stream.Read(buf); err != nil {
					t.Log(stream.id, i, err,
						"session.bucket", atomic.LoadInt32(&session.bucket), // 0 means MaxReceiveBuffer not enough
						"stream.bucket", atomic.LoadInt32(&stream.bucket), // >= MaxStreamBuffer means MaxStreamBuffer not enough or flag not send (bug)
						"stream.empflag", atomic.LoadInt32(&stream.empflag), "stream.fulflag", atomic.LoadInt32(&stream.fulflag))
					dumpGoroutine(t)
					t.Fatal(stream.id, i, err, "since start", time.Since(start))
					return
				} else if string(buf[:n]) != msg {
					t.Fatal(err)
				} else {
					t.Log("[normal]r", stream.id, i, "rtt", time.Since(start),
						"stream.bucket", atomic.LoadInt32(&stream.bucket),
						"stream.guessNeeded", atomic.LoadInt32(&stream.guessNeeded))
				}
				time.Sleep(TestDt)
			}
		} else {
			t.Fatal(err)
		}
	}()
	wg.Wait()
}

func TestReadStreamAfterStreamCloseButRemainData(t *testing.T) {
	s1, s2, err := getSmuxStreamPair(nil)
	if err != nil {
		t.Fatal(err)
	}
	testReadStreamAfterStreamCloseButRemainData(t, s1, s2)
}

func TestReadStreamAfterStreamCloseButRemainDataPipe(t *testing.T) {
	s1, s2, err := getSmuxStreamPairPipe(nil)
	if err != nil {
		t.Fatal(err)
	}
	testReadStreamAfterStreamCloseButRemainData(t, s1, s2)
}

func testReadStreamAfterStreamCloseButRemainData(t *testing.T, s1 *Stream, s2 *Stream) {
	defer s2.Close()

	const N = 10
	var sent string
	var received string

	// send and immediately close
	nsent := 0
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		sent += msg
		n, err := s1.Write([]byte(msg))
		if err != nil {
			t.Fatal("cannot write")
		}
		nsent += n
	}
	s1.Close()

	// read out all remain data
	buf := make([]byte, 10)
	nrecv := 0
	for nrecv < nsent {
		n, err := s2.Read(buf)
		if err == nil {
			received += string(buf[:n])
			nrecv += n
		} else {
			t.Fatal("cannot read remain data", err)
			break
		}
	}

	if sent != received {
		t.Fatal("data mimatch")
	}

	if _, err := s2.Read(buf); err == nil {
		t.Fatal("no error after close and no remain data")
	}
}

func TestReadZeroLengthBuffer(t *testing.T) {
	s1, s2, err := getSmuxStreamPair(nil)
	if err != nil {
		t.Fatal(err)
	}
	testReadZeroLengthBuffer(t, s1, s2)
}

func TestReadZeroLengthBuffer2(t *testing.T) {
	s1, s2, err := getSmuxStreamPairPipe(nil)
	if err != nil {
		t.Fatal(err)
	}
	testReadZeroLengthBuffer(t, s1, s2)
}

func TestReadZeroLengthBuffer3(t *testing.T) {
	s1, s2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
	}
	testReadZeroLengthBuffer(t, s1, s2)
}

func testReadZeroLengthBuffer(t *testing.T, srv net.Conn, cli net.Conn) {
	gotRet := make(chan bool, 1)
	readyRead := make(chan bool, 1)
	go func(){
		buf := make([]byte, 0)
		close(readyRead)
		cli.Read(buf)
		close(gotRet)
	}()

	<-readyRead
	time.Sleep(100 * time.Millisecond)

	select {
	case <-gotRet:
	default:
		t.Fatal("reading zero length buffer should not block")
	}
	srv.Close()
	cli.Close()
}

func TestWriteStreamRace(t *testing.T) {
	config := DefaultConfig()
	config.MaxFrameSize = 1500
	config.EnableStreamBuffer = true
	config.MaxReceiveBuffer = 16 * 1024 * 1024
	config.MaxStreamBuffer = config.MaxFrameSize * 8

	s1, s2, err := getSmuxStreamPair(config)
	if err != nil {
		t.Fatal(err)
	}
	testWriteStreamRace(t, s1, s2, config.MaxFrameSize)
}

func TestWriteStreamRace2(t *testing.T) {
	config := DefaultConfig()
	config.MaxFrameSize = 1500
	config.EnableStreamBuffer = true
	config.MaxReceiveBuffer = 16 * 1024 * 1024
	config.MaxStreamBuffer = config.MaxFrameSize * 8

	s1, s2, err := getSmuxStreamPairPipe(config)
	if err != nil {
		t.Fatal(err)
	}
	testWriteStreamRace(t, s1, s2, config.MaxFrameSize)
}

func TestWriteStreamRaceTCP(t *testing.T) {
	s1, s2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
	}
	testWriteStreamRace(t, s1, s2, 1500) // tcp frame size == 1500 ?
}

func TestWriteStreamRacePipe(t *testing.T) { // go v1.9.x won't pass
	s1, s2 := net.Pipe()
	testWriteStreamRace(t, s1, s2, 1500) // tcp frame size == 1500 ?
}

func testWriteStreamRace(t *testing.T, s1 net.Conn, s2 net.Conn, frameSize int) {
	defer s1.Close()
	defer s2.Close()

	mkMsg := func(char byte, size int) []byte {
		buf := make([]byte, size, size)
		for i := range buf {
			buf[i] = char
		}
		return buf
	}

	MAXSIZE := frameSize * 4
	testMsg := map[byte][]byte{
		'a': mkMsg('a', MAXSIZE),
		'b': mkMsg('b', frameSize * 3),
		'c': mkMsg('c', frameSize * 2),
		'd': mkMsg('d', frameSize),
		'e': mkMsg('e', frameSize / 2),
	}

	// Parallel Write(), data should not reorder in one Write() call
	die := make(chan struct{})
	var wg sync.WaitGroup
	for _, msg := range testMsg {
		wg.Add(1)
		go func(s net.Conn, msg []byte) {
			defer wg.Done()
			for { // keep write untill all stream end
				select {
				case <-die:
					return
				default:
				}
				s.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_, err := s.Write(msg)
				if err != nil {
					//t.Fatal("write data error", err)
					return
				}
			}
		}(s1, msg)
	}

	// read and check data
	const N = 100 * 1000
	buf := make([]byte, MAXSIZE, MAXSIZE)
	for i := 0; i < N; i++ {
		_, err := io.ReadFull(s2, buf[:1])
		if err != nil {
			t.Fatal("cannot read data", err)
			break
		}
		msg := testMsg[buf[0]]
		n, err := io.ReadFull(s2, buf[1:len(msg)])
		if err == nil {
			if bytes.Compare(buf[:n+1], msg) != 0 {
				t.Fatal("data mimatch", n, string(buf[0]))
				break
			}
		} else {
			t.Fatal("cannot read data", err)
			break
		}
	}
	close(die)
	wg.Wait()
}

func TestSmallBufferReadWrite(t *testing.T) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		t.Fatal(err)
		return
	}

	testSmallBufferReadWrite(t, c1, c2)
}

func testSmallBufferReadWrite(t *testing.T, srv net.Conn, cli net.Conn) {
	defer srv.Close()
	defer cli.Close()

	config := &Config{
		KeepAliveInterval:  10000 * time.Millisecond,
		KeepAliveTimeout:   50000 * time.Millisecond,
		MaxFrameSize:       1 * 1024,
		MaxReceiveBuffer:   1 * 1024,
		EnableStreamBuffer: false,
		MaxStreamBuffer:    4 * 1024,
		BoostTimeout:       0 * time.Millisecond,
	}


	go func (conn net.Conn) { // echo server
		session, _ := Server(conn, config)
		for {
			if stream, err := session.AcceptStream(); err == nil {
				go func(s io.ReadWriteCloser) {
					buf := make([]byte, 1024 * 1024, 1024 * 1024)
					for {
						n, err := s.Read(buf)
						if err != nil {
							return
						}
						s.Write(buf[:n])
					}
				}(stream)
			} else {
				return
			}
		}
	}(srv)

	var dumpSess = func (t *testing.T, sess *Session) {
		sess.streamLock.Lock()
		defer sess.streamLock.Unlock()

		t.Log("session.bucket", atomic.LoadInt32(&sess.bucket), "session.streams.len", len(sess.streams))
		for sid, stream := range sess.streams {
			t.Log("stream.id", sid, "stream.bucket", atomic.LoadInt32(&stream.bucket),
				"stream.empflag", atomic.LoadInt32(&stream.empflag), "stream.fulflag", atomic.LoadInt32(&stream.fulflag))
		}
	}

	flag := int32(1)
	var wg sync.WaitGroup

	var test = func(session *Session) { // fast write & slow read
		defer wg.Done()

		stream, err := session.OpenStream()
		if err == nil {
			t.Log("[stream][start]", stream.id)
			defer func() {
				stream.Close()
				t.Log("[stream][end]", stream.id)
			}()

			const SIZE = 8 * 1024 // Bytes
			const SPDW = 16 * 1024 * 1024 // Bytes/s
			const SPDR = 16 * 1024 * 1024// Bytes/s
			const TestDtW = time.Second / time.Duration(SPDW/SIZE)
			const TestDtR = time.Second / time.Duration(SPDR/SIZE)

			var fwg sync.WaitGroup
			fwg.Add(1)
			go func() { // read = SPDR
				defer fwg.Done()
				rbuf := make([]byte, SIZE, SIZE)
				for atomic.LoadInt32(&flag) > 0 {
					stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					if _, err := stream.Read(rbuf); err != nil {
						dumpSess(t, session)
						dumpGoroutine(t)
						t.Fatal("read data error", err)
						break
					}
					time.Sleep(TestDtR) // slow down read
				}
			}()

			buf := make([]byte, SIZE, SIZE)
			for i := range buf {
				buf[i] = byte('-')
			}

			for atomic.LoadInt32(&flag) > 0 { // write = SPDW
				stream.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_, err := stream.Write(buf)
				if err != nil {
					if strings.Contains(err.Error(), "i/o timeout") {
						continue
					}
					dumpSess(t, session)
					t.Fatal("write data error", err)
					break
				}
				time.Sleep(TestDtW) // slow down write
			}
			fwg.Wait()
		} else {
			t.Fatal(err)
		}
	}

	session, _ := Client(cli, config)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go test(session)
	}

	time.Sleep(5 * time.Second)
	atomic.StoreInt32(&flag, int32(0))
	wg.Wait()
}

func BenchmarkAcceptClose(b *testing.B) {
	_, stop, cli, err := setupServer(b)
	if err != nil {
		b.Fatal(err)
	}
	defer stop()
	benchmarkAcceptClose(b, cli)
}

func BenchmarkAcceptClosePipe(b *testing.B) {
	_, stop, cli, err := setupServerPipe(b)
	if err != nil {
		b.Fatal(err)
	}
	defer stop()
	benchmarkAcceptClose(b, cli)
}

func benchmarkAcceptClose(b *testing.B, cli net.Conn) {
	session, _ := Client(cli, nil)
	for i := 0; i < b.N; i++ {
		if stream, err := session.OpenStream(); err == nil {
			stream.Close()
		} else {
			b.Fatal(err)
		}
	}
}

func BenchmarkConnSmux(b *testing.B) {
	config := DefaultConfig()
	config.KeepAliveInterval = 5000 * time.Millisecond
	config.KeepAliveTimeout = 20000 * time.Millisecond
	config.EnableStreamBuffer = false

	cs, ss, err := getSmuxStreamPair(config)
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnSmuxEnableStreamToken(b *testing.B) {
	config := DefaultConfig()
	config.KeepAliveInterval = 50 * time.Millisecond
	config.KeepAliveTimeout = 200 * time.Millisecond
	config.EnableStreamBuffer = true

	cs, ss, err := getSmuxStreamPair(config)
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnSmuxPipe(b *testing.B) {
	config := DefaultConfig()
	config.KeepAliveInterval = 5000 * time.Millisecond
	config.KeepAliveTimeout = 20000 * time.Millisecond
	config.EnableStreamBuffer = false

	cs, ss, err := getSmuxStreamPairPipe(config)
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnSmuxPipeEnableStreamToken(b *testing.B) {
	config := DefaultConfig()
	config.KeepAliveInterval = 50 * time.Millisecond
	config.KeepAliveTimeout = 200 * time.Millisecond
	config.EnableStreamBuffer = true

	cs, ss, err := getSmuxStreamPairPipe(config)
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnTCP(b *testing.B) {
	cs, ss, err := getTCPConnectionPair()
	if err != nil {
		b.Fatal(err)
	}
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func BenchmarkConnPipe(b *testing.B) {
	cs, ss := net.Pipe()
	defer cs.Close()
	defer ss.Close()
	bench(b, cs, ss)
}

func getSmuxStreamPair(config *Config) (*Stream, *Stream, error) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		return nil, nil, err
	}
	return getSmuxStreamPairInternal(c1, c2, config)
}

func getSmuxStreamPairPipe(config *Config) (*Stream, *Stream, error) {
	c1, c2 := net.Pipe()
	return getSmuxStreamPairInternal(c1, c2, config)
}

func getSmuxStreamPairInternal(c1, c2 net.Conn, config *Config) (*Stream, *Stream, error) {
	s, err := Server(c2, config)
	if err != nil {
		return nil, nil, err
	}
	c, err := Client(c1, config)
	if err != nil {
		return nil, nil, err
	}
	var ss *Stream
	done := make(chan error)
	go func() {
		var rerr error
		ss, rerr = s.AcceptStream()
		done <- rerr
		close(done)
	}()
	cs, err := c.OpenStream()
	if err != nil {
		return nil, nil, err
	}
	err = <-done
	if err != nil {
		return nil, nil, err
	}

	return cs, ss, nil
}

func getTCPConnectionPair() (net.Conn, net.Conn, error) {
	lst, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, nil, err
	}
	defer lst.Close()

	var conn0 net.Conn
	var err0 error
	done := make(chan struct{})
	go func() {
		conn0, err0 = lst.Accept()
		close(done)
	}()

	conn1, err := net.Dial("tcp", lst.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	<-done
	if err0 != nil {
		return nil, nil, err0
	}
	return conn0, conn1, nil
}

func bench(b *testing.B, rd io.Reader, wr io.Writer) {
	buf := make([]byte, 128*1024)
	buf2 := make([]byte, 128*1024)
	b.SetBytes(128 * 1024)
	b.ResetTimer()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			n, err := rd.Read(buf2)
			if err != nil {
				b.Fatal("Read()", err)
			}
			count += n
			if count == 128*1024*b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		wr.Write(buf)
	}
	wg.Wait()
}

func dumpGoroutine(t *testing.T) {
	var b bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&b, 2)
	t.Log(b.String())
}

