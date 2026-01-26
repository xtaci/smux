package smux

import "testing"

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
