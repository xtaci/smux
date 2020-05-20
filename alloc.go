package smux

import (
	"errors"
	"sync"
)

var (
	defaultAllocator *Allocator
	debruijinPos     = [...]byte{0, 1, 28, 2, 29, 14, 24, 3, 30, 22, 20, 15, 25, 17, 4, 8, 31, 27, 13, 23, 21, 19, 16, 7, 26, 12, 18, 6, 11, 5, 10, 9}
)

func init() {
	defaultAllocator = NewAllocator()
}

// Allocator for incoming frames, optimized to prevent overwriting after zeroing
type Allocator struct {
	buffers []sync.Pool
}

// NewAllocator initiates a []byte allocator for frames less than 65536 bytes,
// the waste(memory fragmentation) of space allocation is guaranteed to be
// no more than 50%.
func NewAllocator() *Allocator {
	alloc := new(Allocator)
	alloc.buffers = make([]sync.Pool, 17) // 1B -> 64K
	for k := range alloc.buffers {
		i := k
		alloc.buffers[k].New = func() interface{} {
			return make([]byte, 1<<uint32(i))
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
	if size == 1<<bits {
		return alloc.buffers[bits].Get().([]byte)[:size]
	} else {
		return alloc.buffers[bits+1].Get().([]byte)[:size]
	}
}

// Put returns a []byte to pool for future use,
// which the cap must be exactly 2^n
func (alloc *Allocator) Put(buf []byte) error {
	bits := msb(cap(buf))
	if cap(buf) == 0 || cap(buf) > 65536 || cap(buf) != 1<<bits {
		return errors.New("allocator Put() incorrect buffer size")
	}
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
	v = (v >> 1) + 1
	return debruijinPos[(v*0x077CB531)>>27]
}
