package smux

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	ln, err := net.Listen("tcp", "127.0.0.1:19999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				// handle error
			}
			go handleConnection(conn)
		}
	}()
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
			log.Println(err)
			return
		}
	}
}

func TestEcho(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	buf := make([]byte, 10)
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		fmt.Println("sent:", msg)
		stream.Write([]byte(msg))
		if n, err := stream.Read(buf); err == nil {
			fmt.Println("recv:", string(buf[:n]))
		} else {
			return
		}
	}
	session.Close()
}

func TestSpeed(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		buf := make([]byte, 1024*1024)
		nrecv := 0
		for {
			n, err := stream.Read(buf)
			if err != nil {
				fmt.Println(err)
				break
			} else {
				nrecv += n
				if nrecv == 4096*4096 {
					break
				}
			}
		}
		println("total recv:", nrecv)
		stream.Close()
		fmt.Println("time for 16MB rtt", time.Now().Sub(start))
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
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
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

func TestCloseThenOpen(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	session.Close()
	if _, err := session.OpenStream(); err == nil {
		t.Fatal("opened after close")
	}
}

func TestTinyReadBuffer(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	stream, _ := session.OpenStream()
	const N = 100
	tinybuf := make([]byte, 6)
	for i := 0; i < N; i++ {
		msg := fmt.Sprintf("hello%v", i)
		fmt.Println("sent:", msg)
		nsent, err := stream.Write([]byte(msg))
		if err != nil {
			t.Fatal("cannot write")
		}
		nrecv := 0
		for nrecv < nsent {
			fmt.Println(nrecv, nsent)
			if n, err := stream.Read(tinybuf); err == nil {
				fmt.Println("recv:", string(tinybuf[:n]))
				nrecv += n
			} else {
				t.Fatal("cannot read with tiny buffer")
			}
		}
		fmt.Println("#", nrecv, nsent)
	}
	session.Close()
}

func TestIsClose(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, nil)
	session.Close()
	if session.IsClosed() != true {
		t.Fatal("still open after close")
	}
}

func TestKeepAliveTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:29999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		ln.Accept()
	}()

	cli, err := net.Dial("tcp", "127.0.0.1:29999")
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultConfig()
	config.KeepAliveInterval = 1
	config.KeepAliveTimeout = 2
	session, _ := Client(cli, config)
	<-time.After(3 * time.Second)
	if session.IsClosed() != true {
		t.Fatal("keepalive-timeout failed")
	}
}

func TestServerEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:39999")
	if err != nil {
		// handle error
		panic(err)
	}
	go func() {
		if conn, err := ln.Accept(); err == nil {
			session, _ := Server(conn, nil)
			if stream, err := session.OpenStream(); err == nil {
				const N = 100
				buf := make([]byte, 10)
				for i := 0; i < N; i++ {
					msg := fmt.Sprintf("hello%v", i)
					fmt.Println("sent:", msg)
					stream.Write([]byte(msg))
					if n, err := stream.Read(buf); err == nil {
						fmt.Println("recv:", string(buf[:n]))
					} else {
						return
					}
				}
				stream.Close()
			} else {
				t.Fatal(err)
			}
		} else {
			t.Fatal(err)
		}
	}()

	cli, err := net.Dial("tcp", "127.0.0.1:39999")
	if err != nil {
		t.Fatal(err)
	}
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

func BenchmarkAcceptClose(b *testing.B) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		b.Fatal(err)
	}
	session, _ := Client(cli, nil)
	for i := 0; i < b.N; i++ {
		if stream, err := session.OpenStream(); err == nil {
			stream.Close()
		} else {
			b.Fatal(err)
		}
	}
}
