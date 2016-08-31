package smux

import "testing"

func TestFrame(t *testing.T) {
	x := newFrame(cmdSYN, 1234)
	bts_x, _ := x.MarshalBinary()
	y := Frame{}
	y.UnmarshalBinary(bts_x)
	bts_y, _ := y.MarshalBinary()

	if rawHeader(bts_x).String() != rawHeader(bts_y).String() {
		t.Fatal("frame encode/decode failed")
	}
	t.Log(rawHeader(bts_x).String())
}
