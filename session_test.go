package smux

import (
	"fmt"
	"net"
	"testing"
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
		stream, _ := session.Accept()
		go func(stream net.Conn) {
			for {
				n, err := stream.Read(buf)
				if err != nil {
					panic(err)
				}
				fmt.Println("server recv:", string(buf[:n]), n)
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
			panic(err)
		}
	}
}
