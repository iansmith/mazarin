// diplomat/main/mmap.go - Memory allocation for Go runtime
//
// Implements mmap logic using spans and UEFI memory allocation.
// Called from runtime·mmap in sys_linux_amd64.s

package main

// mmap constants (from Linux)
const (
	MAP_FIXED     = 0x10
	PROT_READ     = 0x1
	PROT_WRITE    = 0x2
	PROT_EXEC     = 0x4
	ENOMEM        = 12
	EINVAL        = 22
)

// Bump allocator region (fallback when no hint or hint fails)
// This is a fixed 2GB region starting at 4GB
// We pre-register this as a span during initialization
const (
	bumpRegionStart = uintptr(0x100000000) // 4GB
	bumpRegionSize  = uintptr(0x80000000)  // 2GB
	bumpRegionEnd   = bumpRegionStart + bumpRegionSize
)

var bumpNext uintptr = bumpRegionStart

// mmapCallCount tracks how many mmap calls we've seen
// Used for debugging and loop detection
var mmapCallCount uint32

// DiplomatMmap implements mmap for the Go runtime.
// This is called from runtime·mmap via a Go->ASM->Go chain.
//
// Parameters match Linux mmap(2):
//   addr   - requested address (0 = any, non-zero = hint or MAP_FIXED)
//   length - size in bytes
//   prot   - protection flags (PROT_READ/WRITE/EXEC)
//   flags  - mapping flags (MAP_FIXED, etc.)
//   fd     - file descriptor (ignored, we only do anonymous)
//   offset - file offset (ignored)
//
// Returns:
//   >= 0: allocated address
//   <  0: -errno (e.g., -ENOMEM, -EINVAL)
//
// Strategy (same as Cardinal):
// 1. MAP_FIXED: Must use exact address or fail
// 2. Hint provided: Try to honor it
// 3. No hint: Use bump allocator
//
//go:nosplit
func DiplomatMmap(addr uintptr, length uint64, prot int32, flags int32, fd int32, offset int64) int64 {
	mmapCallCount++

	// Loop detection - hang if called too many times
	// This catches infinite loops in Go runtime initialization
	if mmapCallCount > 100 {
		for {}
	}

	// Handle zero-length mmap
	// This should never happen, but we'll allocate a minimal page
	if length == 0 {
		length = 4096
		addr = 0 // Force bump allocator
	}

	// Round length up to page boundary
	pageSize := uint64(4096)
	roundedLength := (length + pageSize - 1) &^ (pageSize - 1)

	// ========================================================================
	// Strategy 1: MAP_FIXED - must use exact address
	// ========================================================================
	if (flags & MAP_FIXED) != 0 {
		// Validate address
		if addr == 0 {
			// MAP_FIXED with addr=0 is invalid
			// Fall through to bump allocator (broken caller)
			goto useBumpAllocator
		}

		// Check page alignment
		if (addr & 0xFFF) != 0 {
			return -EINVAL
		}

		// Check for reasonable address range
		// x86_64 canonical addresses: 0 to 0x00007FFFFFFFFFFF
		// or 0xFFFF800000000000 to 0xFFFFFFFFFFFFFFFF
		// For now, restrict to lower 128TB
		const maxVirtAddr = uintptr(0x800000000000)
		if addr >= maxVirtAddr {
			return -ENOMEM
		}

		endAddr := addr + uintptr(roundedLength)
		if endAddr < addr || endAddr > maxVirtAddr {
			return -ENOMEM
		}

		// Check for overlaps with existing spans
		if globalSpanTracker.Overlaps(addr, endAddr) {
			return -ENOMEM
		}

		// Try to allocate via UEFI (if Boot Services available)
		allocAddr, ok := AllocatePagesForMmap(addr, uintptr(roundedLength), true)
		if !ok {
			// UEFI allocation failed
			return -ENOMEM
		}

		// Register the span
		if !globalSpanTracker.Register(allocAddr, allocAddr+uintptr(roundedLength)) {
			return -ENOMEM
		}

		// Return the allocated address
		return int64(allocAddr)
	}

	// ========================================================================
	// Strategy 2: Hint provided - try to honor it
	// ========================================================================
	if addr != 0 {
		// Validate hint is reasonable
		// Go's arena allocator uses addresses in the lower 48-bit space
		// Accept hints up to 256TB
		const maxMappableAddr = uintptr(0x1000000000000) // 256TB

		// Check if hint is page-aligned and in range
		if (addr&0xFFF) == 0 && // Page aligned
			addr+uintptr(roundedLength) >= addr && // No overflow
			addr+uintptr(roundedLength) <= maxMappableAddr {

			// Check if hint is free
			freeAddr := globalSpanTracker.FindFreeRegion(addr, uintptr(roundedLength))

			if freeAddr != 0 {
				// Try to allocate via UEFI at hint address
				allocAddr, ok := AllocatePagesForMmap(freeAddr, uintptr(roundedLength), false)
				if ok {
					// Register and return
					if !globalSpanTracker.Register(allocAddr, allocAddr+uintptr(roundedLength)) {
						return -ENOMEM
					}
					return int64(allocAddr)
				}
			}
		}
		// Hint failed - fall through to bump allocator
	}

useBumpAllocator:
	// ========================================================================
	// Strategy 3: No hint or hint failed - use bump allocator
	// ========================================================================

	// Try UEFI allocation without hint
	allocAddr, ok := AllocatePagesForMmap(0, uintptr(roundedLength), false)
	if ok {
		// UEFI allocation succeeded
		if !globalSpanTracker.Register(allocAddr, allocAddr+uintptr(roundedLength)) {
			return -ENOMEM
		}
		return int64(allocAddr)
	}

	// UEFI failed - fall back to bump allocator
	// This happens after ExitBootServices when UEFI is no longer available
	allocAddr = bumpNext
	endAddr := allocAddr + uintptr(roundedLength)

	// Check if allocation would overflow the bump region
	if endAddr > bumpRegionEnd || endAddr < allocAddr {
		return -ENOMEM
	}

	// Register the span
	if !globalSpanTracker.Register(allocAddr, endAddr) {
		return -ENOMEM
	}

	// Update bump pointer for next allocation
	bumpNext = endAddr

	return int64(allocAddr)
}

// DiplomatMunmap implements munmap (currently a stub)
// We don't actually free memory - just return success
//
//go:nosplit
func DiplomatMunmap(addr uintptr, length uint64) int64 {
	// Success - we don't actually reclaim memory
	return 0
}

// DiplomatMadvise implements madvise (stub)
//
//go:nosplit
func DiplomatMadvise(addr uintptr, length uint64, advice int32) int64 {
	// Success - we ignore advice
	return 0
}

// DiplomatBrk implements brk (stub)
// Returns a fixed address - Go runtime doesn't use brk much
//
//go:nosplit
func DiplomatBrk(addr uintptr) int64 {
	// Return fixed break address (16GB)
	const fixedBrk = int64(0x400000000)
	return fixedBrk
}

// InitializeSpans pre-registers the bump allocator region
// Called during diplomat initialization before Go runtime starts
//
//go:nosplit
func InitializeSpans() bool {
	// Pre-register the bump allocator region
	// This is a 2GB region starting at 4GB
	// Note: Call concrete type method directly to avoid interface dispatch early in boot
	return globalSpanTrackerImpl.Register(bumpRegionStart, bumpRegionEnd)
}
