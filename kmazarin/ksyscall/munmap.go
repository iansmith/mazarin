
package ksyscall

import (
	"mazzy/kmazarin/kmem"
)

// SyscallMunmap implements the munmap(2) syscall.
// Unmaps pages in the specified range, updates span tracking,
// and returns physical frames to the buddy allocator via PageDescriptor refcounting.
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

	// Unmap each page and free its physical frame.
	for va := alignedAddr; va < alignedEnd; va += pageSize {
		pa := kmem.UnmapUserPage(uintptr(va))
		if pa != 0 {
			// Free the physical frame via PageDescriptor refcount.
			// Skips pages outside the pool (diplomat-mapped segments, MMIO).
			kmem.ReleasePageByPA(pa)
		}
	}

	return 0
}
