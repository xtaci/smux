package smux

import (
	"errors"
	"sync"
)

var (
	lowBitsMap  = make([]byte, 257)
	highBitsMap = make([]byte, 257)

	defaultAllocator *Allocator
)

func init() {
	defaultAllocator = NewAllocator()

	for i := 1; i <= 256; i++ {
		var pos int
		var size int = i
		size >>= 1
		for pos = 0; size > 0; pos++ {
			size >>= 1
		}
		if i > (1 << pos) {
			pos += 1
		}
		lowBitsMap[i] = byte(pos)
		highBitsMap[i] = (byte(pos) + 8)
	}
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

	return alloc.buffers[msb(size)].Get().([]byte)[:size]
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
func msb(size int) byte {
	if size == 65536 {
		return 16
	}

	if size < 256 {
		return lowBitsMap[size&0xFF]
	}

	pos := highBitsMap[(size&0xFF00)>>8]
	if size > (1 << pos) {
		pos += 1
	}
	return pos
}
