//go:build qemuvirt && aarch64

package ksyscall

// SyscallMmap implements the mmap(2) syscall
// For now, we implement a very simple bump allocator
// TODO: Implement proper memory management later
//
//go:nosplit
func SyscallMmap(addr, length, prot, flags, fd, offset uint64) int64 {
	// For now, ignore addr hint and just allocate from a bump pointer
	// This is very simplified - real mmap needs:
	// - Respect MAP_FIXED if addr != 0 and MAP_FIXED is set
	// - Track allocated regions
	// - Handle file-backed mappings (fd != -1)
	// - Handle different protections

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	// Get memory from bump allocator
	result := bumpAlloc(alignedLength)
	if result == 0 {
		return -12 // ENOMEM
	}

	return int64(result)
}

// Simple bump allocator for mmap
// Starts at 0x10000000 (256MB) and grows upward
// TODO: Replace with proper memory management
var bumpPointer uint64 = 0x10000000

//go:nosplit
func bumpAlloc(size uint64) uint64 {
	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Check if we have space (crude check - just make sure we don't overflow)
	if bumpPointer > 0x80000000 { // Don't go above 2GB
		return 0
	}

	result := bumpPointer
	bumpPointer += aligned

	return result
}
