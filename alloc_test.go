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
	"math/bits"
	"math/rand"
	"testing"
)

func TestAllocGet(t *testing.T) {
	alloc := NewAllocator()
	if alloc.Get(0) != nil {
		t.Fatal(0)
		return
	}
	if len(*alloc.Get(1)) != 1 {
		t.Fatal(1)
		return
	}
	if len(*alloc.Get(2)) != 2 {
		t.Fatal(2)
		return
	}
	if len(*alloc.Get(3)) != 3 || cap(*alloc.Get(3)) != 4 {
		t.Fatal(3)
		return
	}
	if len(*alloc.Get(4)) != 4 {
		t.Fatal(4)
		return
	}
	if len(*alloc.Get(1023)) != 1023 || cap(*alloc.Get(1023)) != 1024 {
		t.Fatal(1023)
		return
	}
	if len(*alloc.Get(1024)) != 1024 {
		t.Fatal(1024)
		return
	}
	if len(*alloc.Get(65536)) != 65536 {
		t.Fatal(65536)
		return
	}
	if alloc.Get(65537) != nil {
		t.Fatal(65537)
		return
	}
}

func TestAllocPut(t *testing.T) {
	alloc := NewAllocator()
	if err := alloc.Put(nil); err == nil {
		t.Fatal("put nil misbehavior")
		return
	}
	b := make([]byte, 3)
	if err := alloc.Put(&b); err == nil {
		t.Fatal("put elem:3 []bytes misbehavior")
		return
	}
	b = make([]byte, 4)
	if err := alloc.Put(&b); err != nil {
		t.Fatal("put elem:4 []bytes misbehavior")
		return
	}
	b = make([]byte, 1023, 1024)
	if err := alloc.Put(&b); err != nil {
		t.Fatal("put elem:1024 []bytes misbehavior")
		return
	}
	b = make([]byte, 65536)
	if err := alloc.Put(&b); err != nil {
		t.Fatal("put elem:65536 []bytes misbehavior")
		return
	}
	b = make([]byte, 65537)
	if err := alloc.Put(&b); err == nil {
		t.Fatal("put elem:65537 []bytes misbehavior")
		return
	}
}

func TestAllocPutThenGet(t *testing.T) {
	alloc := NewAllocator()
	data := alloc.Get(4)
	alloc.Put(data)
	newData := alloc.Get(4)
	if cap(*data) != cap(*newData) {
		t.Fatal("different cap while alloc.Get()")
		return
	}
}

func BenchmarkMSB(b *testing.B) {
	for i := 0; i < b.N; i++ {
		msb(rand.Int())
	}
}

func BenchmarkAlloc(b *testing.B) {
	for i := 0; i < b.N; i++ {
		pbuf := defaultAllocator.Get(i % 65536)
		defaultAllocator.Put(pbuf)
	}
}

func TestDebrujin(t *testing.T) {
	for i := 1; i <= 65536; i++ {
		a := int(msb(i))
		b := bits.Len(uint(i))
		if a+1 != b {
			t.Fatal("debrujin")
			return
		}
	}
}

func TestAllocPutSizeMismatch(t *testing.T) {
	alloc := NewAllocator()
	data := alloc.Get(1024)
	if data == nil {
		t.Fatal("Get(1024) failed")
	}

	// simulate a slice operation that reduces capacity
	// e.g. data[1:]
	// cap becomes 1023
	sliced := (*data)[1:]
	if err := alloc.Put(&sliced); err == nil {
		t.Fatal("Put() should fail with mismatched capacity")
	}

	// simulate a slice operation that keeps capacity
	// e.g. data[:512]
	// cap remains 1024
	sliced = (*data)[:512]
	if err := alloc.Put(&sliced); err != nil {
		t.Fatal("Put() should succeed with matched capacity")
	}
}
