// cardinal/main/dmalloc.go
// Simple bump allocator for cardinal - no Go runtime heap dependencies.
// Used for FAT32 filesystem and directory walking during boot.
package main

import "unsafe"

// Heap region - 64KB for boot-time allocations
// Gets thrown away once kmazarin starts
var cHeap [64 * 1024]byte
var cHeapOffset uintptr

// cMalloc allocates size bytes from cardinal's boot heap.
// Returns a pointer to the allocated memory, or nil if out of space.
// Memory is zero-initialized (it's in .bss).
//
//go:nosplit
func cMalloc(size uintptr) unsafe.Pointer {
	// Align to 8 bytes
	aligned := (size + 7) &^ 7

	if cHeapOffset+aligned > uintptr(len(cHeap)) {
		return nil // out of memory
	}

	ptr := unsafe.Pointer(&cHeap[cHeapOffset])
	cHeapOffset += aligned
	return ptr
}

// cNew allocates space for a value of type T and returns a pointer to it.
// The memory is zero-initialized.
func cNew[T any]() *T {
	var zero T
	size := unsafe.Sizeof(zero)
	ptr := cMalloc(size)
	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// cMallocAllocator returns an Allocator function for FAT32's MountWith.
func cMallocAllocator(size uintptr) unsafe.Pointer {
	return cMalloc(size)
}
