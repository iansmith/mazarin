package main

import (
	"unsafe"

	"cardinal/asm"
)

// Page table allocator state - BSS global to avoid circular dependency with heap
var pageTableAllocatorState_global pageTableAllocatorState

// pageTableAllocatorState is the layout of allocator state
type pageTableAllocatorState struct {
	base   uintptr // Base address of page table region (PAGE_TABLE_BASE)
	offset uintptr // Current offset from base (increments by 4KB per allocation)
}

// getPageTableAllocator returns pointer to the allocator state
//
//go:nosplit
func getPageTableAllocator() *pageTableAllocatorState {
	return &pageTableAllocatorState_global
}

// allocatePageTable allocates a 4KB-aligned page table from the reserved region
// Returns the physical address of the allocated table
//
// Implementation details:
// - Uses a simple bump allocator (linear allocation, no free/reuse)
// - Allocates from the reserved region at PAGE_TABLE_BASE (0x5F100000)
// - Each allocation is 4KB (TABLE_SIZE = 4096 bytes)
// - Automatically zeros the allocated table
// - Checks for overflow (ensures we don't exceed PAGE_TABLE_SIZE)
// - Returns 0 on failure (should never happen if calculations are correct)
//
//go:nosplit
func allocatePageTable() uintptr {
	alloc := getPageTableAllocator()

	// Calculate next allocation address
	ptr := alloc.base + alloc.offset

	// Verify 4KB alignment (should always be true, but check anyway)
	if (ptr & 0xFFF) != 0 {
		// Fatal error - use direct UART write
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'A'
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'L'
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'N'
		for {
		} // Halt on alignment error
	}

	// Check for overflow (ensure we don't exceed allocated region)
	if alloc.offset+TABLE_SIZE > PAGE_TABLE_SIZE {
		// Fatal error - use direct UART write
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'O'
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'V'
		*(*uint32)(unsafe.Pointer(uintptr(0x09000000))) = 'R'
		for {
		} // Halt on overflow
	}

	// Zero the allocated table (required - page tables must start empty)
	bzero4K(unsafe.Pointer(ptr), TABLE_SIZE)

	// Ensure all memory writes from bzero are visible before returning
	asm.Dsb()

	// Update allocator state for next allocation
	alloc.offset += TABLE_SIZE

	return ptr
}

// getPageTableAllocatorStats returns allocation statistics (for debugging)
//
//go:nosplit
func getPageTableAllocatorStats() (allocated uintptr, remaining uintptr) {
	alloc := getPageTableAllocator()
	allocated = alloc.offset
	if allocated > PAGE_TABLE_SIZE {
		remaining = 0
	} else {
		remaining = PAGE_TABLE_SIZE - allocated
	}
	return
}

// =============================================================================
// Physical Frame Allocator (for demand paging)
// =============================================================================

// Physical frame allocator state - BSS global to avoid circular dependency with heap
var physFrameAllocatorState_global physFrameAllocatorState

// physFrameAllocatorState is the layout of allocator state
type physFrameAllocatorState struct {
	next       uintptr // Next physical frame to allocate
	end        uintptr // End of physical frame pool
	pagesAlloc uint32  // Total pages allocated (for 1GB limit check)
	padding    uint32  // Alignment padding
}

// getPhysFrameAllocator returns pointer to the allocator state
//
//go:nosplit
func getPhysFrameAllocator() *physFrameAllocatorState {
	return &physFrameAllocatorState_global
}

// Total kernel pages - BSS global to avoid circular dependency with heap
var totalKernelPages_global uint32

// Page fault handler re-entrancy guard
// CRITICAL: This MUST be in BSS (pre-mapped before MMU enable) to avoid
// triggering a page fault when checking for nested faults!
var inPageFaultHandler_global uint32

//go:nosplit
func getTotalKernelPages() uint32 {
	return totalKernelPages_global
}

//go:nosplit
func setTotalKernelPages(v uint32) {
	totalKernelPages_global = v
}

//go:nosplit
func incTotalKernelPages() {
	totalKernelPages_global++
}

// initPhysFrameAllocator initializes the physical frame allocator
// Uses fixed address storage to avoid being zeroed by memInit
//
//go:nosplit
func initPhysFrameAllocator() {
	// Calculate physical frame pool start from linker symbols
	// Physical frames start after kmazarin executable
	// Since kmazarin hasn't been loaded yet, use conservative estimate
	// CRITICAL: Called from initMMU() before MMU is enabled, so use direct assembly call
	kmazarinLoadAddr := asm.GetKmazarinLoadAddr()
	physFrameBase := kmazarinLoadAddr + KMAZARIN_CONSERVATIVE_SIZE // ~0x42000000

	alloc := getPhysFrameAllocator()
	alloc.next = physFrameBase
	alloc.end = PHYS_FRAME_END
	alloc.pagesAlloc = 0

	// Calculate pre-mapped pages
	// DTB (1MB) + Cardinal (15MB) + Page Tables (8MB) + Kmazarin (~8MB) = ~32MB pre-mapped
	// Note: Actual kmazarin size may be less, this is conservative
	preMappedBytes := uintptr(physFrameBase - 0x40000000)
	preMappedPages := uint32(preMappedBytes / PAGE_SIZE)
	setTotalKernelPages(preMappedPages)

	poolSize := PHYS_FRAME_END - physFrameBase
	poolPages := poolSize / PAGE_SIZE

	// Suppress verbose output - physical frame allocator ready
	_ = poolPages
}

// allocPhysFrame allocates a single 4KB physical frame
// Returns 0 if no more frames available or over 1GB limit
//
//go:nosplit
func allocPhysFrame() uintptr {
	alloc := getPhysFrameAllocator()
	totalPages := getTotalKernelPages()

	// Check 1GB kernel limit FIRST
	if totalPages >= MAX_KERNEL_PAGES {
		uartPutsDirect("\r\nMMU: OVER MEMORY THRESHOLD!\r\n")
		uartPutsDirect("MMU: Kernel has used ")
		uartPutHex64Direct(uint64(totalPages))
		uartPutsDirect(" pages (limit: ")
		uartPutHex64Direct(uint64(MAX_KERNEL_PAGES))
		uartPutsDirect(" = 1GB)\r\n")
		uartPutsDirect("MMU: ABORT - reduce heap usage or increase limit\r\n")
		return 0
	}

	// Check physical frame pool
	if alloc.next >= alloc.end {
		uartPutsDirect("\r\nMMU: Physical frame pool exhausted!\r\n")
		uartPutsDirect("MMU: next=0x")
		uartPutHex64Direct(uint64(alloc.next))
		uartPutsDirect(" end=0x")
		uartPutHex64Direct(uint64(alloc.end))
		uartPutsDirect(" pagesAlloc=0x")
		uartPutHex64Direct(uint64(alloc.pagesAlloc))
		uartPutsDirect("\r\n")
		return 0
	}

	frame := alloc.next
	alloc.next += PAGE_SIZE
	alloc.pagesAlloc++
	incTotalKernelPages()

	totalPages = getTotalKernelPages()

	// NOTE: Frame is NOT zeroed here to avoid nested page faults
	// The caller (HandlePageFault) will zero it after validating the address
	// but before mapping it to the virtual address space

	return frame
}

// getPhysFrameStats returns physical frame allocation stats
//
//go:nosplit
func getPhysFrameStats() (totalPages, demandPages, remaining uint32) {
	alloc := getPhysFrameAllocator()
	totalPages = getTotalKernelPages()
	demandPages = alloc.pagesAlloc
	if totalPages >= MAX_KERNEL_PAGES {
		remaining = 0
	} else {
		remaining = MAX_KERNEL_PAGES - totalPages
	}
	return
}

