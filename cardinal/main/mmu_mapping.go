package main

import (
	"unsafe"

	"mazzy/cardinal/asm"
)

// createPageTableEntry creates a page table entry
// addr: Physical address (must be 4KB aligned)
// attrs: Memory attributes (PTE_ATTR_NORMAL or PTE_ATTR_DEVICE)
// ap: Access permissions (PTE_AP_RW_EL1, etc.)
// exec: Execute permissions (PTE_EXEC_ALLOW or PTE_EXEC_NEVER)
//
//go:nosplit
func createPageTableEntry(addr uintptr, attrs uint64, ap uint64, exec uint64) uint64 {
	// Create page table entry
	// NOTE: L3 page descriptors must have bits[1:0] = 0b11, so include PTE_TABLE here.
	// Use Inner Shareable (SH=3) to match TCR_EL1.SH0 setting
	//
	// CRITICAL: addr MUST be page-aligned (low 12 bits = 0)
	// If addr has low bits set, they will corrupt the attribute fields!
	// Go's linker doesn't guarantee section page-alignment, so mapRegionInitMMU
	// must page-align addresses before calling this function.
	entry := uint64(addr) | PTE_VALID | PTE_TABLE | PTE_AF | attrs | ap | exec | PTE_SH_INNER
	return entry
}

// createTableEntry creates a table descriptor (points to next level)
// nextTable: Physical address of next-level table (must be 4KB aligned)
//
//go:nosplit
func createTableEntry(nextTable uintptr) uint64 {
	entry := uint64(nextTable) | PTE_VALID | PTE_TABLE
	return entry
}

// mapPage maps a single 4KB page
// va: Virtual address (must be 4KB aligned)
// pa: Physical address (must be 4KB aligned)
// attrs: Memory attributes
// ap: Access permissions
//
// LAZY ALLOCATION: L3 tables are allocated on-demand when first page in a 2MB region is mapped.
// This allows us to fit 16MB of theoretical page tables into 15MB by only allocating what's needed.
//
// mapPageInitMMU is used during initMMU (before MMU enabled, Go runtime not ready)
// This version can use more stack because it's NOT called from exception handlers
// It avoids morestack by being called from non-nosplit initMMU
// mapPageDebugCount tracks how many pages have been mapped during initMMU
var mapPageDebugCount uint32

func mapPageInitMMU(va, pa uintptr, attrs uint64, ap uint64, exec uint64) {
	uartBase := uintptr(0x09000000)

	mapPageDebugCount++

	// Extract level indices from virtual address
	// Use uint64 to ensure 64-bit arithmetic (uintptr might be 32 bits in some builds)
	va64 := uint64(va)

	// Use explicit shift values to avoid any constant folding issues
	// Note: Indices can be 0-511 (9 bits), so we need uint16, not uint8
	l0Idx := uint16((va64 >> 39) & 0x1FF) // Bits 48-39
	l1Idx := uint16((va64 >> 30) & 0x1FF) // Bits 38-30
	l2Idx := uint16((va64 >> 21) & 0x1FF) // Bits 29-21
	l3Idx := uint16((va64 >> 12) & 0x1FF) // Bits 20-12

	// Debug flag - disabled for cleaner output
	doDebug := false

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = 'I' // got Indices
	}

	// Get L0 entry (L0 table is pre-allocated in initMMU)
	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = '0' // got L0 entry
	}

	// For identity mapping, we pre-allocate L1 table in initMMU for L0 entry 0
	// For highmem addresses (L0 index > 0), we need to allocate a new L1 table
	if (*l0Entry & PTE_TABLE) == 0 {
		// L0 entry not set - need to allocate L1 table for this L0 entry
		if l0Idx == 0 {
			// This shouldn't happen - L0 entry 0 should be set in initMMU
			*(*uint32)(unsafe.Pointer(uartBase)) = 'L'
			*(*uint32)(unsafe.Pointer(uartBase)) = '0'
			*(*uint32)(unsafe.Pointer(uartBase)) = 'E'
			return
		}
		// For highmem addresses, allocate a new L1 table
		l1Table := allocatePageTable()
		if l1Table == 0 {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'L'
			*(*uint32)(unsafe.Pointer(uartBase)) = '1'
			*(*uint32)(unsafe.Pointer(uartBase)) = '!'
			return
		}
		*l0Entry = createTableEntry(l1Table)
	}

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = '1' // L0 handled
	}

	// Extract L1 table address from L0 entry
	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK) // Extract PA from PTE (bits 47:12)

	// Update global pageTableL1 for consistency (though we don't use it in this function)
	pageTableL1 = l1Table

	// Get L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = '2' // got L1 entry
	}

	// If L1 entry doesn't point to L2 table, create it
	var l2Table uintptr
	if (*l1Entry & PTE_TABLE) == 0 {
		if doDebug {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'A' // allocating L2
			// Print the ptr we're about to allocate
			alloc := getPageTableAllocator()
			uartPutHex64Direct(uint64(alloc.base + alloc.offset))
			*(*uint32)(unsafe.Pointer(uartBase)) = '/'
		}
		l2Table = allocatePageTable()
		if doDebug {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'B' // L2 allocated
		}
		if l2Table == 0 {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'L'
			*(*uint32)(unsafe.Pointer(uartBase)) = '2'
			*(*uint32)(unsafe.Pointer(uartBase)) = '!'
			return
		}

		*l1Entry = createTableEntry(l2Table)
	} else {
		l2Table = uintptr(*l1Entry & PTE_ADDR_MASK)
	}

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = '3' // L1 handled
	}

	// Get L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))

	// LAZY ALLOCATION: If L2 entry doesn't point to L3 table, create it now
	// This is the key optimization - we only allocate L3 tables when needed
	var l3Table uintptr
	if (*l2Entry & PTE_TABLE) == 0 {
		if doDebug {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'C' // allocating L3
		}
		l3Table = allocatePageTable() // Allocate L3 table on-demand
		if l3Table == 0 {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'L'
			*(*uint32)(unsafe.Pointer(uartBase)) = '3'
			*(*uint32)(unsafe.Pointer(uartBase)) = '!'
			return
		}
		if doDebug {
			*(*uint32)(unsafe.Pointer(uartBase)) = 'D' // L3 allocated
		}

		*l2Entry = createTableEntry(l3Table)
	} else {
		l3Table = uintptr(*l2Entry & PTE_ADDR_MASK)
	}

	if doDebug {
		*(*uint32)(unsafe.Pointer(uartBase)) = '4' // L2 handled
	}

	// Set L3 entry (the actual page)
	l3EntryAddr := l3Table + uintptr(l3Idx)*PTE_SIZE
	l3Entry := (*uint64)(unsafe.Pointer(l3EntryAddr))

	pteValue := createPageTableEntry(pa, attrs, ap, exec)
	*l3Entry = pteValue

	// NOTE: Cache cleaning and barriers moved to end of initMMU() for performance
	// The MMU isn't enabled yet, so page table walker won't see stale cache
}

// mapPage is a minimal nosplit implementation for demand paging from exception handlers
// CRITICAL: This must fit within nosplit stack limits!
// It does the absolute minimum needed to map a single page:
// - Assumes L0/L1 tables are already set up (done during initMMU)
// - Only allocates L2/L3 tables if needed
//
//go:nosplit
func mapPage(va, pa uintptr, attrs uint64, ap uint64, exec uint64) {
	// This calls mapPageInitMMU which is NOT nosplit
	// Go compiler should allow this because mapPage is called from nosplit but
	// mapPageInitMMU is not in the nosplit chain from ExceptionHandler
	// Actually wait - it IS in the chain via HandlePageFault!
	//
	// We need a truly minimal inline implementation here
	va64 := uint64(va)
	l0Idx := uint16((va64 >> 39) & 0x1FF)
	l1Idx := uint16((va64 >> 30) & 0x1FF)
	l2Idx := uint16((va64 >> 21) & 0x1FF)
	l3Idx := uint16((va64 >> 12) & 0x1FF)

	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	if (*l0Entry & PTE_TABLE) == 0 {
		// L0 not set - can't allocate in nosplit context, just fail silently
		uartPutcDirect('!')
		uartPutcDirect('L')
		uartPutcDirect('0')
		return
	}

	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))

	var l2Table uintptr
	if (*l1Entry & PTE_TABLE) == 0 {
		l2Table = allocatePageTable()
		if l2Table == 0 {
			uartPutcDirect('!')
			uartPutcDirect('L')
			uartPutcDirect('2')
			return
		}
		*l1Entry = createTableEntry(l2Table)
	} else {
		l2Table = uintptr(*l1Entry & PTE_ADDR_MASK)
	}

	l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))

	var l3Table uintptr
	if (*l2Entry & PTE_TABLE) == 0 {
		l3Table = allocatePageTable()
		if l3Table == 0 {
			uartPutcDirect('!')
			uartPutcDirect('L')
			uartPutcDirect('3')
			return
		}
		*l2Entry = createTableEntry(l3Table)
	} else {
		l3Table = uintptr(*l2Entry & PTE_ADDR_MASK)
	}

	l3EntryAddr := l3Table + uintptr(l3Idx)*PTE_SIZE
	l3Entry := (*uint64)(unsafe.Pointer(l3EntryAddr))
	*l3Entry = createPageTableEntry(pa, attrs, ap, exec)
}

// unmapPage removes a 4KB page mapping from TTBR0 page tables
// This clears the valid bit in the L3 PTE, causing future accesses to fault.
// Note: Does NOT free the physical frame or invalidate TLB (caller must do that)
func unmapPage(va uintptr) {
	va64 := uint64(va)
	l0Idx := uint16((va64 >> 39) & 0x1FF)
	l1Idx := uint16((va64 >> 30) & 0x1FF)
	l2Idx := uint16((va64 >> 21) & 0x1FF)
	l3Idx := uint16((va64 >> 12) & 0x1FF)

	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	if (*l0Entry & PTE_TABLE) == 0 {
		// L0 not present - page not mapped
		return
	}

	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
	l1EntryAddr := l1Table + uintptr(l1Idx)*PTE_SIZE
	l1Entry := (*uint64)(unsafe.Pointer(l1EntryAddr))

	if (*l1Entry & PTE_TABLE) == 0 {
		// L1 not present - page not mapped
		return
	}

	l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)
	l2EntryAddr := l2Table + uintptr(l2Idx)*PTE_SIZE
	l2Entry := (*uint64)(unsafe.Pointer(l2EntryAddr))

	if (*l2Entry & PTE_TABLE) == 0 {
		// L2 not present - page not mapped
		return
	}

	l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)
	l3EntryAddr := l3Table + uintptr(l3Idx)*PTE_SIZE
	l3Entry := (*uint64)(unsafe.Pointer(l3EntryAddr))

	// Clear valid bit (bit 0) to unmap the page
	*l3Entry &^= 1
}

// mapRegionInitMMU maps a contiguous region during initMMU
// Uses mapPageInitMMU which is NOT nosplit (ok because not in exception handler chain)
// vaStart: Start virtual address (must be 4KB aligned)
// vaEnd: End virtual address (exclusive, must be 4KB aligned)
// paStart: Start physical address (must be 4KB aligned)
// attrs: Memory attributes
// ap: Access permissions
// exec: Execute permissions
func mapRegionInitMMU(vaStart, vaEnd, paStart uintptr, attrs uint64, ap uint64, exec uint64) {
	// Sanity check - detect invalid ranges
	if vaStart >= vaEnd || (vaEnd - vaStart) > 0x100000000 {
		return
	}

	// CRITICAL: Page-align the addresses!
	// Linker symbols may not be page-aligned (e.g., dataStart = 0x402B03C0)
	// We need to map whole pages, so round down to page boundary
	const PAGE_MASK = ^uintptr(PAGE_SIZE - 1) // 0xFFFFFFFFFFFFF000
	va := vaStart & PAGE_MASK
	pa := paStart & PAGE_MASK

	// Also need to extend vaEnd to cover the last partial page
	vaEndAligned := (vaEnd + PAGE_SIZE - 1) & PAGE_MASK

	for va < vaEndAligned {
		mapPageInitMMU(va, pa, attrs, ap, exec)
		va += PAGE_SIZE
		pa += PAGE_SIZE
	}

	// NOTE: Cache cleaning moved to end of initMMU() for performance
	// The MMU isn't enabled yet, so page table walker won't see stale cache
	// We'll clean cache once for all page tables before enabling MMU
}

// getPhysicalAddress walks page tables to get the physical address for a VA
// Returns 0 if not mapped
//
//go:nosplit
func getPhysicalAddress(va uintptr) uintptr {
	va64 := uint64(va)
	l0Idx := uint16((va64 >> 39) & 0x1FF)
	l1Idx := uint16((va64 >> 30) & 0x1FF)
	l2Idx := uint16((va64 >> 21) & 0x1FF)
	l3Idx := uint16((va64 >> 12) & 0x1FF)

	// Walk page tables
	l0Entry := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
	if (*l0Entry & PTE_VALID) == 0 {
		return 0
	}
	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)

	l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))
	if (*l1Entry & PTE_VALID) == 0 {
		return 0
	}
	l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)

	l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))
	if (*l2Entry & PTE_VALID) == 0 {
		return 0
	}
	l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)

	l3Entry := (*uint64)(unsafe.Pointer(l3Table + uintptr(l3Idx)*PTE_SIZE))
	if (*l3Entry & PTE_VALID) == 0 {
		return 0
	}

	// Extract physical address from L3 entry
	pagePA := uintptr(*l3Entry & PTE_ADDR_MASK)
	offset := va & 0xFFF
	return pagePA | offset
}

// Cache line size (initialized from CTR_EL0)
var cacheLineSize uint32

// initCacheLineSize reads and validates the cache line size from CTR_EL0
// Must be called before using bzero with DC ZVA
//go:nosplit
func initCacheLineSize() {
	ctr := asm.ReadCtrEl0()
	// Extract DminLine (bits [19:16]) - log2 of number of words
	dminLine := (ctr >> 16) & 0xF
	// Cache line size = 4 << dminLine (4 bytes per word)
	cacheLineSize = 4 << dminLine

	// Validate: must be a power of 2, between 16 and 2048 bytes
	// Common values: 32, 64, 128, 256 bytes
	// If invalid or too large, disable DC ZVA optimization by setting to 0
	if cacheLineSize < 16 || cacheLineSize > 2048 {
		cacheLineSize = 0 // Disable DC ZVA optimization
		return
	}

	// Check if it's a power of 2
	if (cacheLineSize & (cacheLineSize - 1)) != 0 {
		cacheLineSize = 0 // Not a power of 2, disable DC ZVA
		return
	}

	// Cache line size is valid and DC ZVA can be used
}

// bzeroSimple zeros a memory region without alignment requirements.
// Uses 8-byte writes where possible, falls back to byte writes for unaligned portions.
// This is suitable for BSS section zeroing where addresses may not be page-aligned.
//
//go:nosplit
func bzeroSimple(ptr unsafe.Pointer, size uint32) {
	if size == 0 {
		return
	}

	addr := uintptr(ptr)
	end := addr + uintptr(size)

	// Zero unaligned prefix bytes (up to 8-byte alignment)
	for addr < end && (addr&7) != 0 {
		*(*byte)(unsafe.Pointer(addr)) = 0
		addr++
	}

	// Zero 8-byte aligned middle portion using 64-bit writes
	alignedEnd := end &^ 7
	for addr < alignedEnd {
		*(*uint64)(unsafe.Pointer(addr)) = 0
		addr += 8
	}

	// Zero remaining suffix bytes
	for addr < end {
		*(*byte)(unsafe.Pointer(addr)) = 0
		addr++
	}
}

// bzero4K zeros a memory region using DC ZVA when possible
// CRITICAL: bzero4K is exclusively used to zero entire memory pages before they are mapped
// or right after. Both ptr and size must be page-aligned (4K aligned).
//
//go:nosplit
func bzero4K(ptr unsafe.Pointer, size uint32) {
	if size == 0 {
		return
	}

	addr := uintptr(ptr)

	// Validate page alignment (4K = 0x1000)
	if (addr & 0xFFF) != 0 {
		kernelPanic("bzero4K: address not page-aligned")
	}
	if (uint32(size) & 0xFFF) != 0 {
		kernelPanic("bzero4K: size not page-aligned")
	}

	end := addr + uintptr(size)

	// If cache line size not initialized or size too small, use simple loop
	if cacheLineSize == 0 || size < cacheLineSize {
		// Simple byte-by-byte zeroing for small regions
		for addr < end {
			*(*byte)(unsafe.Pointer(addr)) = 0
			addr++
		}
		return
	}

	// Zero initial unaligned bytes
	alignMask := uintptr(cacheLineSize - 1)
	for addr < end && (addr&alignMask) != 0 {
		*(*byte)(unsafe.Pointer(addr)) = 0
		addr++
	}

	// Zero cache-line-aligned region with DC ZVA
	alignedEnd := end &^ alignMask
	for addr < alignedEnd {
		asm.DcZva(addr)
		addr += uintptr(cacheLineSize)
	}

	// Zero trailing unaligned bytes
	for addr < end {
		*(*byte)(unsafe.Pointer(addr)) = 0
		addr++
	}
}
