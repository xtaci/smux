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
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	go func() {
		log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
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
			return
		}
		go handleConnection(conn)
	}()
	addr = ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		return "", nil, nil, err
	}
	return ln.Addr().String(), func() { ln.Close() }, conn, nil
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

// setupServer starts new server listening on a random localhost port and
// returns address of the server, function to stop the server, new client
// connection to this server or an error.
func setupServerV2(tb testing.TB) (addr string, stopfunc func(), client net.Conn, err error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, nil, err
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleConnectionV2(conn)
	}()
	addr = ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		ln.Close()
		return "", nil, nil, err
	}
	return ln.Addr().String(), func() { ln.Close() }, conn, nil
}

func handleConnectionV2(conn net.Conn) {
	config := DefaultConfig()
	config.Version = 2
	session, _ := Server(conn, config)
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

func TestWriteTo(t *testing.T) {
	const N = 1 << 20
	// server
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		session, _ := Server(conn, nil)
		for {
			if stream, err := session.AcceptStream(); err == nil {
				go func(s io.ReadWriteCloser) {
					numBytes := 0
					buf := make([]byte, 65536)
					for {
						n, err := s.Read(buf)
						if err != nil {
							return
						}
						s.Write(buf[:n])
						numBytes += n

						if numBytes == N {
							s.Close()
							return
						}
					}
				}(stream)
			} else {
				return
			}
		}
	}()

	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// client
	session, _ := Client(conn, nil)
	stream, _ := session.OpenStream()
	sndbuf := make([]byte, N)
	for i := range sndbuf {
		sndbuf[i] = byte(rand.Int())
	}

	go stream.Write(sndbuf)

	var rcvbuf bytes.Buffer
	nw, ew := stream.WriteTo(&rcvbuf)
	if ew != io.EOF {
		t.Fatal(ew)
	}

	if nw != N {
		t.Fatal("WriteTo nw mismatch", nw)
	}

	if !bytes.Equal(sndbuf, rcvbuf.Bytes()) {
		t.Fatal("mismatched echo bytes")
	}
	t.Log(stream)
}

func TestWriteToV2(t *testing.T) {
	config := DefaultConfig()
	config.Version = 2
	const N = 1 << 20
	// server
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		session, _ := Server(conn, config)
		for {
			if stream, err := session.AcceptStream(); err == nil {
				go func(s io.ReadWriteCloser) {
					numBytes := 0
					buf := make([]byte, 65536)
					for {
						n, err := s.Read(buf)
						if err != nil {
							return
						}
						s.Write(buf[:n])
						numBytes += n

						if numBytes == N {
							s.Close()
							return
						}
					}
				}(stream)
			} else {
				return
			}
		}
	}()

	addr := ln.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// client
	session, _ := Client(conn, config)
	stream, _ := session.OpenStream()
	sndbuf := make([]byte, N)
	for i := range sndbuf {
		sndbuf[i] = byte(rand.Int())
	}

	go stream.Write(sndbuf)

	var rcvbuf bytes.Buffer
	nw, ew := stream.WriteTo(&rcvbuf)
	if ew != io.EOF {
		t.Fatal(ew)
	}

	if nw != N {
		t.Fatal("WriteTo nw mismatch", nw, N)
	}

	if !bytes.Equal(sndbuf, rcvbuf.Bytes()) {
		t.Fatal("mismatched echo bytes")
	}

	t.Log(stream)
}

func TestGetDieCh(t *testing.T) {
	cs, ss, err := getSmuxStreamPair()
	if err != nil {
		t.Fatal(err)
	}
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
	session, _ := Client(cli, nil)

	par := 1000
	messages := 100
	var wg sync.WaitGroup
	wg.Add(par)
	for i := 0; i < par; i++ {
		stream, _ := session.OpenStream()
		go func(s *Stream) {
			buf := make([]byte, 20)
			for j := 0; j < messages; j++ {
				msg := fmt.Sprintf("hello%v", j)
				s.Write([]byte(msg))
				if _, err := s.Read(buf); err != nil {
					break
				}
			}
			s.Close()
			wg.Done()
		}(stream)
	}
	t.Log("created", session.NumStreams(), "streams")
	wg.Wait()
	session.Close()
}

func TestParallelV2(t *testing.T) {
	config := DefaultConfig()
	config.Version = 2
	_, stop, cli, err := setupServerV2(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, config)

	par := 1000
	messages := 100
	var wg sync.WaitGroup
	wg.Add(par)
	for i := 0; i < par; i++ {
		stream, _ := session.OpenStream()
		go func(s *Stream) {
			buf := make([]byte, 20)
			for j := 0; j < messages; j++ {
				msg := fmt.Sprintf("hello%v", j)
				s.Write([]byte(msg))
				if _, err := s.Read(buf); err != nil {
					break
				}
			}
			s.Close()
			wg.Done()
		}(stream)
	}
	t.Log("created", session.NumStreams(), "streams")
	wg.Wait()
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

func TestSessionDoubleClose(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	session.Close()
	if err := session.Close(); err == nil {
		t.Fatal("session double close doesn't return error")
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
		t.Fatal("stream double close doesn't return error")
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
	numStreams := 100
	streams := make([]*Stream, 0, numStreams)
	var wg sync.WaitGroup
	wg.Add(numStreams)
	for i := 0; i < 100; i++ {
		stream, _ := session.OpenStream()
		streams = append(streams, stream)
	}
	for _, s := range streams {
		stream := s
		go func() {
			stream.Close()
			wg.Done()
		}()
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

	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	config := DefaultConfig()
	config.KeepAliveInterval = time.Second
	config.KeepAliveTimeout = 2 * time.Second
	session, _ := Client(cli, config)
	time.Sleep(3 * time.Second)
	if !session.IsClosed() {
		t.Fatal("keepalive-timeout failed")
	}
}

type blockWriteConn struct {
	net.Conn
}

func (c *blockWriteConn) Write(b []byte) (n int, err error) {
	forever := time.Hour * 24
	time.Sleep(forever)
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
	//when writeFrame block, keepalive in old version never timeout
	blockWriteCli := &blockWriteConn{cli}

	config := DefaultConfig()
	config.KeepAliveInterval = time.Second
	config.KeepAliveTimeout = 2 * time.Second
	session, _ := Client(blockWriteCli, config)
	time.Sleep(3 * time.Second)
	if !session.IsClosed() {
		t.Fatal("keepalive-timeout failed")
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
		f := newFrame(1, cmdSYN, 1000)
		session.writeControlFrame(f)
	}
	cli.Close()

	// random cmds
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	allcmds := []byte{cmdSYN, cmdFIN, cmdPSH, cmdNOP}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(1, allcmds[rand.Int()%len(allcmds)], rand.Uint32())
		session.writeControlFrame(f)
	}
	cli.Close()

	// random cmds & sids
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(1, byte(rand.Uint32()), rand.Uint32())
		session.writeControlFrame(f)
	}
	cli.Close()

	// random version
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(1, byte(rand.Uint32()), rand.Uint32())
		f.ver = byte(rand.Uint32())
		session.writeControlFrame(f)
	}
	cli.Close()

	// incorrect size
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	session, _ = Client(cli, nil)

	f := newFrame(1, byte(rand.Uint32()), rand.Uint32())
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
		f := newFrame(1, byte(rand.Uint32()), rand.Uint32())
		session.writeControlFrame(f)
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
		f := newFrame(1, byte(rand.Uint32()), rand.Uint32())

		timer := time.NewTimer(session.config.KeepAliveTimeout)
		defer timer.Stop()

		session.writeFrameInternal(f, timer.C, CLSDATA)
	}

	// random cmds
	cli, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	allcmds := []byte{cmdSYN, cmdFIN, cmdPSH, cmdNOP}
	session, _ = Client(cli, nil)
	for i := 0; i < 100; i++ {
		f := newFrame(1, allcmds[rand.Int()%len(allcmds)], rand.Uint32())

		timer := time.NewTimer(session.config.KeepAliveTimeout)
		defer timer.Stop()

		session.writeFrameInternal(f, timer.C, CLSDATA)
	}
	//deadline occur
	{
		c := make(chan time.Time)
		close(c)
		f := newFrame(1, allcmds[rand.Int()%len(allcmds)], rand.Uint32())
		_, err := session.writeFrameInternal(f, c, CLSDATA)
		if !strings.Contains(err.Error(), "timeout") {
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
		session, _ = Client(&blockWriteConn{cli}, config)
		f := newFrame(1, byte(rand.Uint32()), rand.Uint32())
		c := make(chan time.Time)
		go func() {
			//die first, deadline second, better for coverage
			time.Sleep(time.Second)
			session.Close()
			time.Sleep(time.Second)
			close(c)
		}()
		_, err = session.writeFrameInternal(f, c, CLSDATA)
		if !strings.Contains(err.Error(), "closed pipe") {
			t.Fatal("write frame with to closed conn failed", err)
		}
	}
}

func TestReadDeadline(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
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
		if !strings.Contains(readErr.Error(), "timeout") {
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
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	buf := make([]byte, 10)
	var writeErr error
	for {
		stream.SetWriteDeadline(time.Now().Add(-1 * time.Minute))
		if _, writeErr = stream.Write(buf); writeErr != nil {
			if !strings.Contains(writeErr.Error(), "timeout") {
				t.Fatalf("Wrong error: %v", writeErr)
			}
			break
		}
	}
	session.Close()
}

func Test8GBTransferV1(t *testing.T) {
	_, stop, cli, err := setupServer(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 8 << 30 // 8GB

	// Initialize random sources with the same seed for writer and reader
	seed := time.Now().UnixNano()
	writerSrc := rand.NewSource(seed)
	readerSrc := rand.NewSource(seed)
	sndbuf := make([]byte, 1<<24) // 16MB buffer
	rcvbuf := make([]byte, 1<<24) // 16MB buffer
	bytesSent := int64(0)
	bytesReceived := int64(0)
	// Writer goroutine
	go func() {
		r := rand.New(writerSrc)
		for bytesSent < N {
			// Fill buffer with random data
			for i := range sndbuf {
				sndbuf[i] = byte(r.Int())
			}
			toWrite := int64(len(sndbuf))
			if N-bytesSent < toWrite {
				toWrite = N - bytesSent
			}
			n, err := stream.Write(sndbuf[:toWrite])
			if err != nil {
				t.Errorf("Write error: %v", err)
				return
			}
			bytesSent += int64(n)
			if bytesSent%(1<<30) == 0 { // Log every 1GB
				t.Log("Sent:", bytesSent, "bytes")
			}
		}
		//stream.Close()
	}()
	// Reader goroutine
	r := rand.New(readerSrc)
	for bytesReceived < N {
		toRead := int64(len(rcvbuf))
		if N-bytesReceived < toRead {
			toRead = N - bytesReceived
		}
		n, err := stream.Read(rcvbuf[:toRead])
		if err != nil && err != io.EOF {
			t.Fatalf("Read error: %v", err)
		}
		// Validate received data
		for i := 0; i < n; i++ {
			expectedByte := byte(r.Int())
			if rcvbuf[i] != expectedByte {
				t.Fatalf("Data mismatch at byte %d: got %v, want %v", bytesReceived+int64(i), rcvbuf[i], expectedByte)
			}
		}
		bytesReceived += int64(n)
		if bytesReceived%(1<<30) == 0 { // Log every 1GB
			t.Log("Received:", bytesReceived, "bytes")
		}
	}
	session.Close()
}

// This test validates large data transfer (8GB) over a single stream with random data
func Test8GBTransferV2(t *testing.T) {
	config := DefaultConfig()
	config.Version = 2
	_, stop, cli, err := setupServerV2(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	session, _ := Client(cli, config)
	stream, _ := session.OpenStream()
	const N = 8 << 30 // 8GB

	// Initialize random sources with the same seed for writer and reader
	seed := time.Now().UnixNano()
	writerSrc := rand.NewSource(seed)
	readerSrc := rand.NewSource(seed)

	sndbuf := make([]byte, 1<<24) // 16MB buffer
	rcvbuf := make([]byte, 1<<24) // 16MB buffer

	bytesSent := int64(0)
	bytesReceived := int64(0)

	// Writer goroutine
	go func() {
		r := rand.New(writerSrc)
		for bytesSent < N {
			// Fill buffer with random data
			for i := range sndbuf {
				sndbuf[i] = byte(r.Int())
			}

			toWrite := int64(len(sndbuf))
			if N-bytesSent < toWrite {
				toWrite = N - bytesSent
			}
			n, err := stream.Write(sndbuf[:toWrite])
			if err != nil {
				t.Errorf("Write error: %v", err)
				return
			}
			bytesSent += int64(n)

			if bytesSent%(1<<30) == 0 { // Log every 1GB
				t.Log("Sent:", bytesSent, "bytes")
			}
		}
		//stream.Close()
	}()

	// Reader goroutine
	r := rand.New(readerSrc)
	for bytesReceived < N {
		toRead := int64(len(rcvbuf))
		if N-bytesReceived < toRead {
			toRead = N - bytesReceived
		}
		n, err := stream.Read(rcvbuf[:toRead])
		if err != nil && err != io.EOF {
			t.Fatalf("Read error: %v", err)
		}

		// Validate received data
		for i := 0; i < n; i++ {
			expectedByte := byte(r.Int())
			if rcvbuf[i] != expectedByte {
				t.Fatalf("Data mismatch at byte %d: got %v, want %v", bytesReceived+int64(i), rcvbuf[i], expectedByte)
			}
		}
		bytesReceived += int64(n)
		if bytesReceived%(1<<30) == 0 { // Log every 1GB
			t.Log("Received:", bytesReceived, "bytes")
		}
	}
	session.Close()
}

func BenchmarkAcceptClose(b *testing.B) {
	_, stop, cli, err := setupServer(b)
	if err != nil {
		b.Fatal(err)
	}
	defer stop()
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
	cs, ss, err := getSmuxStreamPair()
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

func getSmuxStreamPair() (*Stream, *Stream, error) {
	c1, c2, err := getTCPConnectionPair()
	if err != nil {
		return nil, nil, err
	}

	s, err := Server(c2, nil)
	if err != nil {
		return nil, nil, err
	}
	c, err := Client(c1, nil)
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
	b.ReportAllocs()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			n, _ := rd.Read(buf2)
			count += n
			if count == 128*1024*b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		wr.Write(buf)
		// invalidate L3 Cache
		buf = make([]byte, 128*1024)
	}
	wg.Wait()
}
