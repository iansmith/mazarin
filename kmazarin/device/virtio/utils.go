
package virtio

import (
	"unsafe"
)

// Helper functions for VirtIO device drivers

// CastToPointer casts a uintptr to a typed pointer
//
//go:nosplit
func CastToPointer[T any](addr uintptr) *T {
	return (*T)(unsafe.Pointer(addr))
}

// PointerToUintptr converts an unsafe.Pointer to uintptr
//
//go:nosplit
func PointerToUintptr(ptr unsafe.Pointer) uintptr {
	return uintptr(ptr)
}

// kmallocAllocations keeps allocated slices alive to prevent GC collection.
// When kmalloc returns an unsafe.Pointer to a slice's backing array, the slice
// header goes out of scope. Without this, the GC could collect the backing array.
// Fixed-size array prevents unbounded growth from append's slice doubling.
var kmallocAllocations [256][]byte
var kmallocCount int

// kmalloc allocates memory using Go's runtime
// For kmazarin, we just use make() to allocate a byte slice
func kmalloc(size uint32) unsafe.Pointer {
	if size == 0 {
		return nil
	}
	// Allocate byte slice and return pointer to first element
	buf := make([]byte, size)
	if len(buf) == 0 {
		return nil
	}
	// Keep slice alive by storing it in a fixed-size global array
	// This prevents GC from collecting the backing array
	if kmallocCount < len(kmallocAllocations) {
		kmallocAllocations[kmallocCount] = buf
		kmallocCount++
	}
	return unsafe.Pointer(&buf[0])
}

// kfree frees memory (no-op in Go, garbage collector handles this)
//
//go:nosplit
func kfree(ptr unsafe.Pointer) {
	// No-op: Go's garbage collector will handle memory freeing
}

// Bzero4K zeros a memory region
// Note: In kmazarin we can be more flexible with alignment than Cardinal
//
//go:nosplit
func Bzero4K(ptr unsafe.Pointer, size uint32) {
	if size == 0 || ptr == nil {
		return
	}

	// Zero memory byte by byte
	p := (*[1 << 30]byte)(ptr)
	for i := uint32(0); i < size; i++ {
		p[i] = 0
	}
}
