package smux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

const (
	maxFrames = 4096
	frameSize = 4096
)

func init() {
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
	session, _ := Server(conn, maxFrames, frameSize)
	for {
		stream, _ := session.AcceptStream()
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
	}
}

func TestEcho(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli, maxFrames, frameSize)
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
	session, _ := Client(cli, maxFrames, frameSize)
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
	session, _ := Client(cli, maxFrames, frameSize)

	par := 1000
	messages := 100
	var wg sync.WaitGroup
	wg.Add(par)
	fmt.Println("testing parallel", par, "connections")
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
	wg.Wait()
	session.Close()
}
