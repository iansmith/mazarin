// Package ringbuf provides a shared-memory SPSC ring buffer for IPC
// between shepherds. The ring is laid out on a memory page so both
// producer and consumer access the same physical memory.
//
// Layout on the page:
//
//	offset 0:  head     (uint32, atomic — consumer advances)
//	offset 4:  tail     (uint32, atomic — producer advances)
//	offset 8:  slotSize (uint32, immutable after init)
//	offset 12: slotCount(uint32, immutable after init)
//	offset 16: slot data begins (slotCount * slotSize bytes)
package ringbuf

import (
	"sync/atomic"
	"unsafe"
)

// headerSize is the byte size of the ring buffer header.
const headerSize = 16

// Header is the in-memory layout of the ring buffer header.
// Placed at the start of the ring buffer region.
type Header struct {
	Head      uint32 // consumer position (atomic)
	Tail      uint32 // producer position (atomic)
	SlotSize  uint32 // bytes per slot (immutable)
	SlotCount uint32 // number of slots (immutable, must be power of 2)
}

// RingBuffer is a handle to a shared-memory ring buffer.
// Both producer and consumer create a RingBuffer pointing at the
// same physical page (via different VAs).
type RingBuffer struct {
	hdr      *Header
	dataBase uintptr // VA of first slot (hdr + headerSize)
}

// Init initializes a ring buffer at the given address.
// This must be called by the creator (producer side) before any use.
// slotSize is the byte size of each message slot.
// slotCount must be a power of 2.
// Returns a RingBuffer handle.
func Init(addr uintptr, slotSize, slotCount uint32) *RingBuffer {
	hdr := (*Header)(unsafe.Pointer(addr))
	hdr.Head = 0
	hdr.Tail = 0
	hdr.SlotSize = slotSize
	hdr.SlotCount = slotCount
	return &RingBuffer{
		hdr:      hdr,
		dataBase: addr + headerSize,
	}
}

// Open opens an existing ring buffer at the given address.
// Called by the consumer side — reads slotSize/slotCount from the header.
func Open(addr uintptr) *RingBuffer {
	hdr := (*Header)(unsafe.Pointer(addr))
	return &RingBuffer{
		hdr:      hdr,
		dataBase: addr + headerSize,
	}
}

// Addr returns the base address of this ring buffer (start of header).
func (rb *RingBuffer) Addr() uintptr {
	return uintptr(unsafe.Pointer(rb.hdr))
}

// Push copies slotSize bytes from src into the next available slot.
// Returns false if the ring is full (message dropped).
func (rb *RingBuffer) Push(src unsafe.Pointer) bool {
	tail := atomic.LoadUint32(&rb.hdr.Tail)
	head := atomic.LoadUint32(&rb.hdr.Head)
	if tail-head >= rb.hdr.SlotCount {
		return false // full
	}
	idx := tail & (rb.hdr.SlotCount - 1)
	dst := rb.dataBase + uintptr(idx)*uintptr(rb.hdr.SlotSize)
	memcpy(dst, uintptr(src), uintptr(rb.hdr.SlotSize))
	atomic.StoreUint32(&rb.hdr.Tail, tail+1)
	return true
}

// Pop copies slotSize bytes from the next available slot into dst.
// Returns false if the ring is empty.
func (rb *RingBuffer) Pop(dst unsafe.Pointer) bool {
	head := atomic.LoadUint32(&rb.hdr.Head)
	tail := atomic.LoadUint32(&rb.hdr.Tail)
	if head == tail {
		return false // empty
	}
	idx := head & (rb.hdr.SlotCount - 1)
	src := rb.dataBase + uintptr(idx)*uintptr(rb.hdr.SlotSize)
	memcpy(uintptr(dst), src, uintptr(rb.hdr.SlotSize))
	atomic.StoreUint32(&rb.hdr.Head, head+1)
	return true
}

// Len returns the number of items currently in the ring.
func (rb *RingBuffer) Len() uint32 {
	tail := atomic.LoadUint32(&rb.hdr.Tail)
	head := atomic.LoadUint32(&rb.hdr.Head)
	return tail - head
}

// memcpy copies n bytes from src to dst. Both must be valid.
//
//go:nosplit
func memcpy(dst, src, n uintptr) {
	d := unsafe.Slice((*byte)(unsafe.Pointer(dst)), n)
	s := unsafe.Slice((*byte)(unsafe.Pointer(src)), n)
	copy(d, s)
}
