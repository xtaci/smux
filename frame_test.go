package smux

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestFrame(t *testing.T) {
	x := newFrame(cmdSYN, 1234)
	x.data = make([]byte, 128)
	io.ReadFull(rand.Reader, x.data)
	btsX, _ := x.MarshalBinary()

	y := Frame{}
	y.UnmarshalBinary(btsX)
	btsY, _ := y.MarshalBinary()

	if !bytes.Equal(btsX, btsY) {
		t.Fatal("frame encode/decode failed")
	}

	t.Log(rawHeader(btsX).String())
	t.Log(btsX)
}
