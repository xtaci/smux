package smux

import "testing"

func TestFrame(t *testing.T) {
	x := newFrame(cmdSYN, 1234)
	btsX, _ := x.MarshalBinary()
	y := Frame{}
	y.UnmarshalBinary(btsX)
	btsY, _ := y.MarshalBinary()

	if rawHeader(btsX).String() != rawHeader(btsY).String() {
		t.Fatal("frame encode/decode failed")
	}
	t.Log(rawHeader(btsX).String())
}
