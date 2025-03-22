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
	"errors"
	"sync"
)

var (
	defaultAllocator *Allocator
	debruijinPos     = [...]byte{0, 9, 1, 10, 13, 21, 2, 29, 11, 14, 16, 18, 22, 25, 3, 30, 8, 12, 20, 28, 15, 17, 24, 7, 19, 27, 23, 6, 26, 5, 4, 31}
)

func init() {
	defaultAllocator = NewAllocator()
}

// Allocator for incoming frames, optimized to prevent overwriting after zeroing
type Allocator struct {
	buffers []sync.Pool
}

func NewAllocator() *Allocator {
	alloc := new(Allocator)
	alloc.buffers = make([]sync.Pool, 17) // 1B -> 64K
	for k := range alloc.buffers {
		i := k
		alloc.buffers[k].New = func() interface{} {
			return make([]byte, 1<<uint32(i))
		}
		// Pre-warm each pool to reduce initial allocations
		for j := 0; j < 256; j++ {
			alloc.buffers[k].Put(make([]byte, 1<<uint32(i)))
		}
	}
	return alloc
}

// Get a []byte from pool with most appropriate cap
func (alloc *Allocator) Get(size int) []byte {
	if size <= 0 || size > 65536 {
		return nil
	}

	bits := msb(size)
	idx := bits
	if size != 1<<bits {
		idx = bits + 1
	}

	if int(idx) >= len(alloc.buffers) {
		return nil
	}

	buf := alloc.buffers[idx].Get().([]byte)
	return buf[:size]
}

// Put returns a []byte to pool for future use,
// which the cap must be exactly 2^n
func (alloc *Allocator) Put(buf []byte) error {
	capacity := cap(buf)
	if capacity == 0 || capacity > 65536 {
		return errors.New("invalid buffer capacity")
	}

	bits := msb(capacity)
	if capacity != 1<<bits {
		return errors.New("buffer capacity not a power of two")
	}

	// Reset to full capacity
	buf = buf[:capacity]
	alloc.buffers[bits].Put(buf)
	return nil
}

// msb return the pos of most significiant bit
// http://supertech.csail.mit.edu/papers/debruijn.pdf
func msb(size int) byte {
	v := uint32(size)
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return debruijinPos[(v*0x07C4ACDD)>>27]
}
