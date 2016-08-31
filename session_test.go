package smux

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func init() {
	go func() {
		ln, err := net.Listen("tcp", "127.0.0.1:19999")
		if err != nil {
			// handle error
			panic(err)
		}
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
	session, _ := Server(conn)
	buf := make([]byte, 65536)
	count := 0
	for {
		stream, _ := session.AcceptStream()
		go func(stream io.ReadWriteCloser) {
			for {
				n, err := stream.Read(buf)
				if err != nil {
					return
				}
				count++
				stream.Write(buf[:n])
			}
		}(stream)
	}

}

func TestEcho(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli)
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
}

func TestSpeed(t *testing.T) {
	cli, err := net.Dial("tcp", "127.0.0.1:19999")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := Client(cli)
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
	msg := make([]byte, 4096)
	for i := 0; i < 4096; i++ {
		stream.Write(msg)
	}
	wg.Wait()
}
