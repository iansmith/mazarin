
package ksyscall

import (
	"mazzy/kmazarin/kmem"
)

// SyscallMunmap implements the munmap(2) syscall
// Unmaps pages in the specified range and updates span tracking.
// Does NOT currently free physical frames back to the pool.
//
//go:nosplit
func SyscallMunmap(addr, length, _, _, _, _ uint64) int64 {
	if length == 0 {
		return 0 // Nothing to unmap
	}

	// Align addr down and length up to page boundaries
	pageSize := uint64(4096)
	alignedAddr := addr &^ (pageSize - 1)
	alignedEnd := (addr + length + pageSize - 1) &^ (pageSize - 1)
	alignedLength := alignedEnd - alignedAddr

	// Update span tracking (remove/split spans for this range)
	removeSpan(alignedAddr, alignedLength)

	// Count how many pages we actually unmap
	unmappedCount := uint64(0)

	// Unmap each page in the range
	for va := alignedAddr; va < alignedEnd; va += pageSize {
		pa := kmem.UnmapUserPage(uintptr(va))
		if pa != 0 {
			unmappedCount++
			// Note: We don't free the frame back to pool yet
			// This is a memory leak, but prevents use-after-free issues
		}
	}

	_ = unmappedCount

	return 0
}
