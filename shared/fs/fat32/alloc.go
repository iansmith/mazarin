// Package fat32 provides a read-only FAT32 filesystem implementation.
// This file defines the allocation interface used by the filesystem.
package fat32

import "unsafe"

// Allocator is a function that allocates memory of the given size.
// Returns a pointer to zeroed memory, or nil if allocation fails.
// The caller is responsible for ensuring the returned pointer is
// properly aligned for the intended use.
//
// In heap-based environments (kmazarin), this wraps Go's allocator.
// In no-heap environments (diplomat, cardinal), this uses a bump allocator.
type Allocator func(size uintptr) unsafe.Pointer

// AllocatorFunc is the type for the allocation callback.
// This is identical to Allocator but named differently for clarity in APIs.
type AllocatorFunc = Allocator

// goHeapAllocator uses Go's heap for allocation.
// This is the default allocator for normal Go programs.
func goHeapAllocator(size uintptr) unsafe.Pointer {
	// Allocate as []byte and return pointer to first element
	buf := make([]byte, size)
	if len(buf) == 0 {
		return nil
	}
	return unsafe.Pointer(&buf[0])
}

// DefaultAllocator returns the Go heap allocator.
// Use this for normal Go programs with full runtime support.
func DefaultAllocator() Allocator {
	return goHeapAllocator
}

// allocType allocates space for a value of type T using the given allocator.
// Returns nil if allocation fails.
func allocType[T any](alloc Allocator) *T {
	var zero T
	size := unsafe.Sizeof(zero)
	ptr := alloc(size)
	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// allocSlice allocates a slice of length n using the given allocator.
// Returns nil slice if allocation fails.
func allocSlice[T any](alloc Allocator, n int) []T {
	if n <= 0 {
		return nil
	}
	var zero T
	elemSize := unsafe.Sizeof(zero)
	totalSize := elemSize * uintptr(n)

	ptr := alloc(totalSize)
	if ptr == nil {
		return nil
	}

	return unsafe.Slice((*T)(ptr), n)
}
