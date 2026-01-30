// diplomat/main/dmalloc.go
// Simple bump allocator for diplomat - no Go runtime dependencies
package main

import "unsafe"

// Heap region - 64KB should be plenty for diplomat's needs
// This gets thrown away once kmazarin starts
var dHeap [64 * 1024]byte
var dHeapOffset uintptr

// dMalloc allocates size bytes from the diplomat heap.
// Returns a pointer to the allocated memory, or nil if out of space.
// Memory is zero-initialized (it's in .bss).
//
//go:nosplit
func dMalloc(size uintptr) unsafe.Pointer {
	// Align to 8 bytes
	aligned := (size + 7) &^ 7

	if dHeapOffset+aligned > uintptr(len(dHeap)) {
		return nil // out of memory
	}

	ptr := unsafe.Pointer(&dHeap[dHeapOffset])
	dHeapOffset += aligned
	return ptr
}

// dNew allocates space for a value of type T and returns a pointer to it.
// The memory is zero-initialized.
// Usage: fs := dNew[FileSystem]()
//
// Note: This is a generic function that works at compile time,
// so it doesn't need runtime type info.
func dNew[T any]() *T {
	var zero T
	size := unsafe.Sizeof(zero)
	ptr := dMalloc(size)
	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// dMakeSlice creates a slice backed by diplomat heap memory.
// Returns nil slice if allocation fails.
func dMakeSlice[T any](length int) []T {
	var zero T
	elemSize := unsafe.Sizeof(zero)
	totalSize := elemSize * uintptr(length)

	ptr := dMalloc(totalSize)
	if ptr == nil {
		return nil
	}

	// Create slice header pointing to our memory
	return unsafe.Slice((*T)(ptr), length)
}

// dHeapUsed returns how much of the diplomat heap has been used
//
//go:nosplit
func dHeapUsed() uintptr {
	return dHeapOffset
}

// dHeapRemaining returns how much space is left in the diplomat heap
//
//go:nosplit
func dHeapRemaining() uintptr {
	return uintptr(len(dHeap)) - dHeapOffset
}
