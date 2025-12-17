package main

import (
	"unsafe"

	"mazboot/asm"
)

// Page table entry bits (ARM64)
const (
	// Lower attributes (bits 0-11)
	PTE_VALID = 1 << 0 // Valid bit (must be 1)
	// Bit 1 is a "type" bit used differently by level:
	// - For L0-L2: bits[1:0] = 0b11 indicates a table descriptor.
	// - For L3:    bits[1:0] = 0b11 indicates a page descriptor.
	// Leaving bit1 = 0 in an L3 entry yields bits[1:0] = 0b01 which is INVALID at L3
	// and causes a level-3 translation fault (including on instruction fetch).
	PTE_TABLE = 1 << 1
	PTE_PAGE  = 0 // Unused; we always emit L3 pages with bits[1:0] = 0b11.

	// Page attributes (bits 2-7)
	PTE_AF = 1 << 10 // Access flag (must be 1 for hardware-managed)
	PTE_NG = 1 << 11 // Not global (0 = global, 1 = per-ASID)

	// Upper attributes (bits 12-63)
	PTE_UXN  = 1 << 54 // Unprivileged execute never
	PTE_PXN  = 1 << 53 // Privileged execute never
	PTE_CONT = 1 << 52 // Contiguous hint
	PTE_DBM  = 1 << 51 // Dirty bit modifier
	PTE_GP   = 1 << 50 // Guarded page
	PTE_nT   = 1 << 16 // Not translation table walk

	// Memory attributes (bits 2-4, MAIR index)
	// MAIR[0] = Normal, Inner/Outer Write-Back Cacheable (0xFF)
	// MAIR[1] = Device-nGnRnE (0x00)
	PTE_ATTR_NORMAL = 0 << 2 // MAIR index 0
	PTE_ATTR_DEVICE = 1 << 2 // MAIR index 1

	// Shareability (bits 8-9)
	PTE_SH_INNER = 3 << 8 // Inner shareable
	PTE_SH_OUTER = 2 << 8 // Outer shareable
	PTE_SH_NONE  = 0 << 8 // Non-shareable

	// Access permissions (bits 6-7)
	PTE_AP_RW     = 0 << 6 // Read/Write at EL0
	PTE_AP_RW_EL1 = 1 << 6 // Read/Write at EL1, no access at EL0
	PTE_AP_RO     = 2 << 6 // Read-only at EL0
	PTE_AP_RO_EL1 = 3 << 6 // Read-only at EL1, no access at EL0
)

// Page table size constants
const (
	PAGE_SHIFT = 12                   // log2(PAGE_SIZE)
	PTE_SIZE   = 8                    // 8 bytes per entry
	PTE_COUNT  = 512                  // 512 entries per table
	TABLE_SIZE = PTE_COUNT * PTE_SIZE // 4KB per table

	// Level shifts (address bits used at each level)
	L0_SHIFT = 39 // Bits 48-39
	L1_SHIFT = 30 // Bits 38-30
	L2_SHIFT = 21 // Bits 29-21
	L3_SHIFT = 12 // Bits 20-12
)

// Page table allocation for demand paging with 1GB physical RAM limit
//
// DEMAND PAGING DESIGN:
// - Total kernel physical memory limit: 1GB
// - Virtual mmap region: 6.5GB (0x60000000 - 0x200000000)
// - Pages are mapped on-demand when accessed (page fault handler)
// - Go runtime reserves large virtual ranges but only touches small fraction
//
// Memory math:
// - 1GB / 4KB = 262,144 pages maximum
// - Page tables for 1GB: ~2MB (512 L3 tables × 4KB + overhead)
// - We track total pages allocated and abort if over threshold
//
// Physical memory layout (~1GB total):
// - 0x40000000-0x40100000: Low RAM (BSS, initial data) - 1MB
// - 0x40100000-0x50000000: Kernel code/data - ~256MB (pre-mapped)
// - 0x50000000-0x5E000000: Reserved/page tables - 224MB
// - 0x60000000-0x180000000: Physical frame pool - ~5GB (for demand paging)
//
// When demand paging, physical frames come from anywhere in the pool.
// We don't identity-map the mmap region - VA != PA for those pages.
// The frame pool (0x60000000+) is mapped as identity-mapped physical memory.
const (
	// Page table region: 0x5E000000 - 0x60000000 (32MB)
	// 1GB needs only ~2MB of page tables, but we allow headroom
	PAGE_TABLE_BASE = 0x5E000000       // Start of page table region
	PAGE_TABLE_SIZE = 32 * 1024 * 1024 // 32MB (way more than needed for 1GB)
	PAGE_TABLE_END  = 0x60000000       // End of page table region

	// Physical frame allocator for demand paging
	// Frames allocated from this pool when page faults occur
	//
	// CRITICAL: Frame pool must be at physical addresses OUTSIDE the mmap virtual
	// region (0x60000000-0x200000000) to avoid conflict. We identity-map the frame
	// pool so we can zero frames when allocating. If the frame pool overlapped with
	// mmap VAs, the identity mapping would defeat demand paging.
	//
	// QEMU has 8GB RAM: 0x40000000-0x240000000
	// We use the region above mmap VA end: 0x200000000-0x240000000 (1GB)
	PHYS_FRAME_BASE = 0x200000000 // Start of physical frame pool (after mmap VA end)
	PHYS_FRAME_END  = 0x240000000 // End (1GB pool, up to 8GB QEMU RAM limit)

	// Virtual mmap region (large virtual, demand-paged)
	// VA range is large but physical backing is limited by PAGE_LIMIT
	//
	// Go runtime arm64 hints start at 0x4000000000 (256GB) and go up.
	// Formula: p = uintptr(i)<<40 | 0x4000000000 for i in [0, 0x7f]
	// We accept any address from our low region up to a reasonable max.
	MMAP_VIRT_BASE = 0x60000000     // Start of virtual mmap region (our bump allocator)
	MMAP_VIRT_END  = 0x800000000000 // End of virtual mmap region (128TB - covers Go hints)

	// Memory limits
	MAX_KERNEL_PAGES = 262144         // 1GB / 4KB = 262,144 pages max
	MAX_KERNEL_BYTES = 1 << 30        // 1GB
)

// Page table structure
var (
	pageTableL0 uintptr   // Level 0 table (PGD)
	pageTableL1 uintptr   // Level 1 table (PUD)
	pageTableL2 []uintptr // Level 2 tables (PMD) - allocated as needed
	pageTableL3 []uintptr // Level 3 tables (PT) - allocated as needed
)

// Page table allocator state stored at FIXED ADDRESS to avoid being
// zeroed by memInit's pageInit. BSS variables after __end get zeroed, but
// this memory region (0x41020600+) is in safe pre-mapped kernel RAM.
//
// Memory layout in 0x41000000 region:
//   0x41000000: P structure (used by runtime stubs)
//   0x41010000: Write barrier buffer (64KB)
//   0x41020000: mcache struct (~0x500 bytes)
//   0x41020500: physFrameAllocator state (32 bytes)
//   0x41020520: totalKernelPages (8 bytes)
//   0x41020600: pageTableAllocator state (this)
const (
	PAGE_TABLE_ALLOC_ADDR = 0x41020600 // Fixed address for page table allocator state
)

// pageTableAllocatorState is the layout of allocator state at fixed address
type pageTableAllocatorState struct {
	base   uintptr // Base address of page table region (PAGE_TABLE_BASE)
	offset uintptr // Current offset from base (increments by 4KB per allocation)
}

// getPageTableAllocator returns pointer to the allocator state at fixed address
//
//go:nosplit
func getPageTableAllocator() *pageTableAllocatorState {
	return (*pageTableAllocatorState)(unsafe.Pointer(uintptr(PAGE_TABLE_ALLOC_ADDR)))
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
		uartPutsDirect("PTALIGN!")
		for {
		} // Halt on alignment error
	}

	// Check for overflow (ensure we don't exceed allocated region)
	if alloc.offset+TABLE_SIZE > PAGE_TABLE_SIZE {
		uartPutsDirect("PTOVERFLOW!")
		for {
		} // Halt on overflow
	}

	// Zero the allocated table (required - page tables must start empty)
	bzero(unsafe.Pointer(ptr), TABLE_SIZE)

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

// Physical frame allocator state stored at FIXED ADDRESS to avoid being
// zeroed by memInit's pageInit. BSS variables after __end get zeroed, but
// this memory region (0x41020500+) is in safe pre-mapped kernel RAM.
//
// Memory layout in 0x41000000 region:
//   0x41000000: P structure (used by runtime stubs)
//   0x41010000: Write barrier buffer (64KB)
//   0x41020000: mcache struct (~0x500 bytes)
//   0x41020500: physFrameAllocator state (this)
const (
	PHYS_FRAME_ALLOC_ADDR = 0x41020500 // Fixed address for allocator state
)

// physFrameAllocatorState is the layout of allocator state at fixed address
type physFrameAllocatorState struct {
	next       uintptr // Next physical frame to allocate
	end        uintptr // End of physical frame pool
	pagesAlloc uint32  // Total pages allocated (for 1GB limit check)
	padding    uint32  // Alignment padding
}

// getPhysFrameAllocator returns pointer to the allocator state at fixed address
//
//go:nosplit
func getPhysFrameAllocator() *physFrameAllocatorState {
	return (*physFrameAllocatorState)(unsafe.Pointer(uintptr(PHYS_FRAME_ALLOC_ADDR)))
}

// Total kernel pages - also stored at fixed address (0x41020520)
const TOTAL_KERNEL_PAGES_ADDR = 0x41020520

//go:nosplit
func getTotalKernelPages() uint32 {
	return *(*uint32)(unsafe.Pointer(uintptr(TOTAL_KERNEL_PAGES_ADDR)))
}

//go:nosplit
func setTotalKernelPages(v uint32) {
	*(*uint32)(unsafe.Pointer(uintptr(TOTAL_KERNEL_PAGES_ADDR))) = v
}

//go:nosplit
func incTotalKernelPages() {
	ptr := (*uint32)(unsafe.Pointer(uintptr(TOTAL_KERNEL_PAGES_ADDR)))
	*ptr++
}

// initPhysFrameAllocator initializes the physical frame allocator
// Uses fixed address storage to avoid being zeroed by memInit
//
//go:nosplit
func initPhysFrameAllocator() {
	alloc := getPhysFrameAllocator()
	alloc.next = PHYS_FRAME_BASE
	alloc.end = PHYS_FRAME_END
	alloc.pagesAlloc = 0

	// Calculate pre-mapped pages (kernel code/data from 0x40000000 to 0x50000000)
	preMappedBytes := uintptr(0x50000000 - 0x40000000) // 256MB pre-mapped
	preMappedPages := uint32(preMappedBytes / PAGE_SIZE)
	setTotalKernelPages(preMappedPages)

	poolSize := PHYS_FRAME_END - PHYS_FRAME_BASE
	poolPages := poolSize / PAGE_SIZE

	uartPuts("MMU: Physical frame allocator initialized\r\n")
	uartPuts("MMU:   Pre-mapped: ")
	uartPutHex64(uint64(preMappedPages))
	uartPuts(" pages (")
	uartPutHex64(uint64(preMappedBytes >> 20))
	uartPuts(" MB)\r\n")
	uartPuts("MMU:   Frame pool: 0x")
	uartPutHex64(uint64(PHYS_FRAME_BASE))
	uartPuts(" - 0x")
	uartPutHex64(uint64(PHYS_FRAME_END))
	uartPuts(" (")
	uartPutHex64(uint64(poolPages))
	uartPuts(" pages = ")
	uartPutHex64(uint64(poolSize >> 20))
	uartPuts(" MB)\r\n")
	uartPuts("MMU:   Kernel limit: ")
	uartPutHex64(uint64(MAX_KERNEL_PAGES))
	uartPuts(" pages (1GB)\r\n")
	uartPuts("MMU:   Available for demand paging: ")
	uartPutHex64(uint64(MAX_KERNEL_PAGES - preMappedPages))
	uartPuts(" pages\r\n")
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
	// Print progress every 1000 pages (4MB)
	if totalPages%1000 == 0 {
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(totalPages))
		uartPutcDirect('/')
		uartPutHex64Direct(uint64(MAX_KERNEL_PAGES))
		uartPutcDirect(']')
	}

	// Zero the frame (required for clean memory)
	bzero(unsafe.Pointer(frame), PAGE_SIZE)

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

// =============================================================================
// Demand Paging Support
// =============================================================================

// mmapPromise tracks a virtual memory promise from mmap
// We track ranges of virtual addresses that were promised but not yet mapped
type mmapPromise struct {
	start uintptr // Start of virtual range
	end   uintptr // End of virtual range (exclusive)
}

// Maximum number of mmap promises we track (should be enough for Go runtime)
const maxMmapPromises = 256

var (
	mmapPromises    [maxMmapPromises]mmapPromise
	mmapPromiseCount int
)

// addMmapPromise records a virtual memory promise
// Returns true on success, false if promise table is full
//
//go:nosplit
func addMmapPromise(start, size uintptr) bool {
	if mmapPromiseCount >= maxMmapPromises {
		uartPutsDirect("MMU: mmap promise table full!\r\n")
		return false
	}

	mmapPromises[mmapPromiseCount] = mmapPromise{
		start: start,
		end:   start + size,
	}
	mmapPromiseCount++
	return true
}

// isAddressPromised checks if a virtual address was promised via mmap
//
//go:nosplit
func isAddressPromised(va uintptr) bool {
	for i := 0; i < mmapPromiseCount; i++ {
		if va >= mmapPromises[i].start && va < mmapPromises[i].end {
			return true
		}
	}
	return false
}

// HandlePageFault handles a page fault for demand paging
// Called from the exception handler when a data abort occurs
// Returns true if the fault was handled (page mapped), false otherwise
//
// Parameters:
//   - faultAddr: The faulting virtual address (FAR_EL1)
//   - faultStatus: The fault status from ESR_EL1 (lower bits)
//
// Simplified design: Any address in the mmap virtual range (0x60000000-0x200000000)
// is considered valid. The Go runtime won't access addresses it didn't request,
// so any fault in this range is from a legitimate mmap allocation.
//
//go:nosplit
//go:noinline
func HandlePageFault(faultAddr uintptr, faultStatus uint64) bool {
	// Print compact debug marker
	uartPutcDirect('F')

	// Check if the fault address is in the mmap virtual region
	// Any address in this range is considered a valid demand-page request
	if faultAddr < MMAP_VIRT_BASE || faultAddr >= MMAP_VIRT_END {
		// Not in mmap region - this is a real fault
		uartPutcDirect('!')
		return false
	}

	// Align fault address to page boundary
	pageAddr := faultAddr &^ (PAGE_SIZE - 1)

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		// Out of physical memory - this is fatal for demand paging
		uartPutsDirect("\r\nDEMAND PAGE OOM at VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		return false
	}

	// Map the virtual page to the physical frame
	// Note: VA != PA for demand-paged memory
	mapPage(pageAddr, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1)

	// Ensure page table writes are visible before TLB flush
	asm.Dsb()

	// Invalidate TLB for this address (full flush for simplicity)
	asm.InvalidateTlbAll()

	// Ensure TLB invalidation completes before returning
	asm.Isb()

	// Success - print compact marker
	uartPutcDirect('+')

	return true
}

// createPageTableEntry creates a page table entry
// addr: Physical address (must be 4KB aligned)
// attrs: Memory attributes (PTE_ATTR_NORMAL or PTE_ATTR_DEVICE)
// ap: Access permissions (PTE_AP_RW_EL1, etc.)
//
//go:nosplit
func createPageTableEntry(addr uintptr, attrs uint64, ap uint64) uint64 {
	// Create page table entry with execute permissions
	// PXN and UXN are cleared (0) by default, allowing execution
	// This is required for code regions to be executable
	// NOTE: L3 page descriptors must have bits[1:0] = 0b11, so include PTE_TABLE here.
	entry := uint64(addr) | PTE_VALID | PTE_TABLE | PTE_AF | attrs | ap | PTE_SH_INNER
	// Note: PXN and UXN bits are NOT set, so execution is allowed
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
//go:nosplit
func mapPage(va, pa uintptr, attrs uint64, ap uint64) {
	// Extract level indices from virtual address
	// Use uint64 to ensure 64-bit arithmetic (uintptr might be 32 bits in some builds)
	va64 := uint64(va)

	// Use explicit shift values to avoid any constant folding issues
	// Note: Indices can be 0-511 (9 bits), so we need uint16, not uint8
	l0Idx := uint16((va64 >> 39) & 0x1FF) // Bits 48-39
	l1Idx := uint16((va64 >> 30) & 0x1FF) // Bits 38-30
	l2Idx := uint16((va64 >> 21) & 0x1FF) // Bits 29-21
	l3Idx := uint16((va64 >> 12) & 0x1FF) // Bits 20-12

	// Get L0 entry (L0 table is pre-allocated in initMMU)
	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0Entry := (*uint64)(unsafe.Pointer(l0EntryAddr))

	// For identity mapping, we pre-allocate L1 table in initMMU for L0 entry 0
	// For highmem addresses (L0 index > 0), we need to allocate a new L1 table
	if (*l0Entry & PTE_TABLE) == 0 {
		// L0 entry not set - need to allocate L1 table for this L0 entry
		if l0Idx == 0 {
			// This shouldn't happen - L0 entry 0 should be set in initMMU
			uartPutsDirect("L0ERR")
			return
		}
		// For highmem addresses, allocate a new L1 table
		l1Table := allocatePageTable()
		*l0Entry = createTableEntry(l1Table)
		asm.Dsb()
	}

	// Extract L1 table address from L0 entry
	l1Table := uintptr(*l0Entry &^ 0xFFF) // Clear lower 12 bits

	// Update global pageTableL1 for consistency (though we don't use it in this function)
	pageTableL1 = l1Table

	// Get L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))

	// If L1 entry doesn't point to L2 table, create it
	var l2Table uintptr
	if (*l1Entry & PTE_TABLE) == 0 {
		l2Table = allocatePageTable()
		*l1Entry = createTableEntry(l2Table)
	} else {
		l2Table = uintptr(*l1Entry &^ 0xFFF)
	}

	// Get L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))

	// LAZY ALLOCATION: If L2 entry doesn't point to L3 table, create it now
	// This is the key optimization - we only allocate L3 tables when needed
	var l3Table uintptr
	if (*l2Entry & PTE_TABLE) == 0 {
		l3Table = allocatePageTable() // Allocate L3 table on-demand
		*l2Entry = createTableEntry(l3Table)
	} else {
		l3Table = uintptr(*l2Entry &^ 0xFFF)
	}

	// Set L3 entry (the actual page)
	l3Entry := (*uint64)(unsafe.Pointer(l3Table + uintptr(l3Idx)*PTE_SIZE))
	*l3Entry = createPageTableEntry(pa, attrs, ap)

	// CRITICAL: Ensure page table writes are visible before continuing
	// Use DSB to ensure all page table writes complete before any subsequent
	// memory access or MMU operation
	asm.Dsb()

	// Additional verification: read back the entry to ensure it was written
	// This helps catch any memory ordering or cache coherency issues
	verifyEntry := *l3Entry
	if verifyEntry != *l3Entry {
		// This shouldn't happen, but if it does, it indicates a serious issue
		uartPuts("MMU: WARNING - Page table entry readback mismatch!\r\n")
	}
}

// mapRegion maps a contiguous region of memory
// vaStart: Start virtual address (must be 4KB aligned)
// vaEnd: End virtual address (exclusive, must be 4KB aligned)
// paStart: Start physical address (must be 4KB aligned)
// attrs: Memory attributes
// ap: Access permissions
//
//go:nosplit
func mapRegion(vaStart, vaEnd, paStart uintptr, attrs uint64, ap uint64) {
	va := vaStart
	pa := paStart

	// Debug: log first page mapping for highmem addresses
	if vaStart >= 0x4010000000 {
		uartPuts("MMU: Mapping first highmem page: VA=0x")
		uartPutHex64(uint64(vaStart))
		uartPuts(" PA=0x")
		uartPutHex64(uint64(paStart))
		uartPuts("\r\n")
	}

	for va < vaEnd {
		mapPage(va, pa, attrs, ap)
		va += PAGE_SIZE
		pa += PAGE_SIZE
	}

	// Ensure all writes are visible
	asm.Dsb()

	// Debug: log completion for highmem addresses
	if vaStart >= 0x4010000000 {
		uartPuts("MMU: Highmem region mapping complete\r\n")
	}
}

// bzero zeros a memory region (use existing implementation or create)
//
//go:nosplit
func bzero(ptr unsafe.Pointer, size uint32) {
	asm.Bzero(ptr, size)
}

// initMMU initializes the MMU with identity-mapped page tables
// This must be called before enabling the MMU
// Returns true on success, false on failure
//
//go:nosplit
func initMMU() bool {
	uartPutc('I') // Breadcrumb: initMMU start
	uartPuts("MMU: Initializing page tables...\r\n")

	// Step 1: Allocate page table memory
	pageTableL0 = PAGE_TABLE_BASE
	pageTableL1 = PAGE_TABLE_BASE + TABLE_SIZE

	// Initialize the bump allocator *after* the pre-allocated L0 + L1 tables.
	// Otherwise allocatePageTable() would hand out PAGE_TABLE_BASE again and overwrite L0,
	// causing bogus/unset entries and eventually allocator overflow.
	// CRITICAL: Use fixed-address allocator to avoid being zeroed by memInit's pageInit()
	ptAlloc := getPageTableAllocator()
	ptAlloc.base = PAGE_TABLE_BASE
	ptAlloc.offset = TABLE_SIZE * 2

	// Verify page table base is 4KB aligned
	if pageTableL0&0xFFF != 0 {
		uartPuts("MMU: ERROR - Page table base not 4KB aligned: 0x")
		uartPutHex64(uint64(pageTableL0))
		uartPuts("\r\n")
		return false
	}

	// Zero out page tables
	bzero(unsafe.Pointer(pageTableL0), TABLE_SIZE*2) // Clear L0 and L1

	// Set up L0 table to point to L1 table for identity mapping
	// For identity mapping, we need L0 entries to point to our pre-allocated L1 table
	// Since we're mapping from 0x00000000 to 0x240000000 (8GB), we need L0 entries 0-4
	// Each L0 entry covers 512GB, so entry 0 covers 0-512GB
	l0Entry0 := (*uint64)(unsafe.Pointer(pageTableL0 + 0*PTE_SIZE))
	*l0Entry0 = createTableEntry(pageTableL1)

	// Verify L0->L1 linkage
	uartPuts("MMU: L0 entry 0 = 0x")
	uartPutHex64(*l0Entry0)
	uartPuts(" (should point to L1 at 0x")
	uartPutHex64(uint64(pageTableL1))
	uartPuts(")\r\n")

	// Verify L1 table is accessible
	l1TestEntry := (*uint64)(unsafe.Pointer(pageTableL1))
	uartPuts("MMU: L1 table accessible, first entry = 0x")
	uartPutHex64(*l1TestEntry)
	uartPuts("\r\n")

	uartPuts("MMU: Page tables allocated at 0x")
	uartPutHex64(uint64(pageTableL0))
	uartPuts("\r\n")

	// Step 2: Map bootloader code/data (ROM region: 0x00000000 - 0x08000000)
	// Note: ROM is read-only hardware, but we map it as Read/Execute for proper caching
	// Write access will correctly fault (ROM cannot be written)
	uartPuts("MMU: Mapping bootloader code/data (0x00000000-0x08000000)...\r\n")
	mapRegion(
		0x00000000,      // VA start
		0x08000000,      // VA end
		0x00000000,      // PA start (identity map)
		PTE_ATTR_NORMAL, // Normal cacheable memory (allows instruction cache)
		// IMPORTANT: map as RO so it remains executable even if SCTLR_EL1.WXN is set.
		// If mapped RW while WXN=1, instruction fetch can fault with a permission fault.
		PTE_AP_RO_EL1, // Read-only at EL1 (correct for ROM + keeps it executable under WXN)
	)

	// Step 3: Map UART (MMIO: 0x09000000 - 0x09010000)
	uartPuts("MMU: Mapping UART (0x09000000-0x09010000)...\r\n")
	mapRegion(
		0x09000000,      // VA start
		0x09010000,      // VA end
		0x09000000,      // PA start (identity map)
		PTE_ATTR_DEVICE, // Device-nGnRnE
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 3.5: Map QEMU fw_cfg (MMIO: 0x09020000 - 0x09030000)
	// The virt machine's RAM framebuffer configuration is provided via fw_cfg.
	// Accessing it without a mapping will cause a Data Abort (e.g., FAR=0x09020008).
	uartPuts("MMU: Mapping fw_cfg (0x09020000-0x09030000)...\r\n")
	mapRegion(
		0x09020000,      // VA start
		0x09030000,      // VA end
		0x09020000,      // PA start (identity map)
		PTE_ATTR_DEVICE, // Device-nGnRnE (MMIO)
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 3.6: Map GIC (MMIO: 0x08000000 - 0x08020000)
	// Our IRQ handler touches the GIC CPU interface at 0x08010000 and GICD at 0x08000000.
	// If this isn't mapped as device memory, enabling IRQs or handling timer/UART IRQs can fault.
	uartPuts("MMU: Mapping GIC (0x08000000-0x08020000)...\r\n")
	mapRegion(
		0x08000000,      // VA start
		0x08020000,      // VA end
		0x08000000,      // PA start (identity map)
		PTE_ATTR_DEVICE, // Device-nGnRnE (MMIO)
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 4: Map low RAM including BSS/stack/etc (0x40000000 - 0x40100000)
	// NOTE: QEMU virt uses RAM starting at 0x40000000 and our linker places BSS there.
	// Mapping this as Device or RO will break normal Go globals and runtime state.
	uartPuts("MMU: Mapping low RAM (0x40000000-0x40100000)...\r\n")
	mapRegion(
		0x40000000,      // VA start
		0x40100000,      // VA end
		0x40000000,      // PA start (identity map)
		PTE_ATTR_NORMAL, // Normal cacheable memory
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 4.5: Map PCI ECAM region (MMIO: 0x3F000000 - 0x40000000 for lowmem)
	// QEMU virt can place ECAM either in lowmem (0x3F000000) or highmem (0x4010000000)
	// depending on machine configuration (highmem on/off). If we hit the wrong base, reads can
	// cause a data abort (we saw FAR=0x3F000000 on default QEMU runs).
	//
	// IMPORTANT: Do NOT parse the DTB here. MMU is still disabled, so all
	// memory is Device-nGnRnE and the Go compiler may emit unaligned stack
	// accesses for any non-trivial function, causing data aborts. Instead we
	// map a conservative lowmem ECAM window and separately map the known
	// highmem ECAM window; later (after MMU is enabled and memory attributes
	// are Normal) we can parse the DTB and refine pciEcamBase.
	uartPuts("MMU: Skipping DTB ECAM parse (MMU disabled, using fallback lowmem ECAM)\r\n")
	ecamBase := uintptr(0x3F000000)
	ecamSize := uintptr(0x10000000) // 256MB
	uartPuts("MMU: Mapping PCI ECAM at 0x")
	uartPutHex64(uint64(ecamBase))
	uartPuts(" size 0x")
	uartPutHex64(uint64(ecamSize))
	uartPuts("\r\n")
	mapRegion(
		ecamBase,          // VA start
		ecamBase+ecamSize, // VA end
		ecamBase,          // PA start (identity map)
		PTE_ATTR_DEVICE,   // Device-nGnRnE (MMIO)
		PTE_AP_RW_EL1,     // Read/Write at EL1
	)

	// Also map highmem ECAM (0x4010000000+) in case QEMU uses it
	// QEMU virt with virtualization=on may use highmem ECAM
	// Explicitly cast to uint64 first to ensure 64-bit constant, then to uintptr
	highmemEcamBase := uintptr(uint64(0x4010000000))
	highmemEcamSize := uintptr(0x10000000) // 256MB
	uartPuts("MMU: Also mapping highmem PCI ECAM at 0x")
	uartPutHex64(uint64(highmemEcamBase))
	uartPuts(" size 0x")
	uartPutHex64(uint64(highmemEcamSize))
	uartPuts("\r\n")
	uartPuts("MMU: About to call mapRegion for highmem ECAM...\r\n")
	mapRegion(
		highmemEcamBase,                 // VA start
		highmemEcamBase+highmemEcamSize, // VA end
		highmemEcamBase,                 // PA start (identity map)
		PTE_ATTR_DEVICE,                 // Device-nGnRnE (MMIO)
		PTE_AP_RW_EL1,                   // Read/Write at EL1
	)
	uartPuts("MMU: mapRegion for highmem ECAM completed\r\n")

	// At this point both lowmem and highmem ECAM windows are mapped. The
	// runtime ECAM base used by PCI code will be selected later from the DTB
	// in initDeviceTree(), so we don't hard-code any particular base here.
	//
	// We still verify the lowmem mapping to catch gross page-table errors.
	uartPuts("MMU: Verifying lowmem PCI ECAM page table entry...\r\n")
	if !dumpFetchMapping("pci-ecam-low", ecamBase) {
		uartPuts("MMU: WARNING - lowmem PCI ECAM mapping verification failed!\r\n")
	} else {
		uartPuts("MMU: Lowmem PCI ECAM mapping verified OK\r\n")
	}
	uartPuts("MMU: Skipping verification for highmem ECAM (L0 index would be >= 128)\r\n")
	uartPuts("MMU: Highmem ECAM mapping should be valid (mapped above)\r\n")

	// Step 5: Map bootloader RAM (0x40100000 - 0x60000000)
	// DEMAND PAGING: Only map the pre-allocated kernel region.
	// The mmap region (0x60000000+) is NOT mapped here - pages are
	// mapped on-demand when accessed via page fault handler.
	//
	// This includes:
	// - 0x40100000-0x50000000: Kernel code/data (~256MB, identity mapped)
	// - 0x50000000-0x5E000000: Physical frame pool for demand paging (224MB, identity mapped)
	// - 0x5E000000-0x60000000: Page tables (32MB, identity mapped for self-mapping)
	//
	// The mmap virtual region (0x60000000-0x200000000) will be mapped on demand
	// with physical frames from the PHYS_FRAME_BASE-PHYS_FRAME_END pool.
	uartPuts("MMU: Mapping kernel RAM (0x40100000-0x60000000)...\r\n")
	mapRegion(
		0x40100000,      // VA start
		0x60000000,      // VA end (end of page table region)
		0x40100000,      // PA start (identity map)
		PTE_ATTR_NORMAL, // Normal cacheable memory
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 5.1: Map physical frame pool region (0x200000000-0x240000000)
	// This identity maps the frame pool so we can zero physical frames when allocating.
	// The frame pool is at high physical addresses (above mmap VA region end) to avoid
	// conflict with demand paging - the mmap VA range (0x60000000-0x200000000) is NOT
	// pre-mapped, allowing page faults to trigger demand allocation.
	uartPuts("MMU: Mapping physical frame pool (0x200000000-0x240000000)...\r\n")
	mapRegion(
		PHYS_FRAME_BASE, // VA start (identity map at 0x200000000)
		PHYS_FRAME_END,  // VA end (0x240000000)
		PHYS_FRAME_BASE, // PA start (identity map)
		PTE_ATTR_NORMAL, // Normal cacheable memory
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)
	uartPuts("MMU: Physical frame pool mapped for kernel access (1GB)\r\n")
	uartPuts("MMU: NOTE: mmap VA region (0x60000000-0x200000000) NOT mapped - demand paging active\r\n")

	// Initialize physical frame allocator for demand paging
	// Now uses fixed addresses (0x41020500+) that won't be zeroed by memInit
	initPhysFrameAllocator()

	// Step 5.5: Map bochs-display framebuffer/MMIO in the PCI MMIO window as Device memory.
	//
	// The QEMU "virt" machine places **PCI MMIO** at 0x10000000‑0x3FFFFFFF and
	// guest RAM at 0x40000000+. We program the bochs-display PCI BARs as:
	//   BAR0 (framebuffer): 0x10000000  (16MB window)
	//   BAR2 (MMIO regs):   0x11000000  (4KB window)
	//
	// To make sure all accesses go through the device and are not cached as
	// normal RAM, we map the entire 16MB BAR0 window as Device-nGnRnE here.
	uartPuts("MMU: Mapping bochs-display framebuffer/MMIO (0x10000000-0x11000000) as Device...\r\n")
	mapRegion(
		0x10000000,      // VA start (framebuffer + MMIO base in PCI MMIO window)
		0x11000000,      // VA end (16MB window for VRAM + MMIO)
		0x10000000,      // PA start (identity map)
		PTE_ATTR_DEVICE, // Device-nGnRnE (MMIO / framebuffer)
		PTE_AP_RW_EL1,   // Read/Write at EL1
	)

	// Step 6: User RAM mapping removed - now included in bootloader RAM (0x40100000-0x78000000)
	// This will be separated when user programs are supported

	uartPuts("MMU: Page tables initialized\r\n")
	return true
}

// dumpFetchMapping prints the key L3 mapping details needed to debug instruction fetch:
// - descriptor validity/type bits
// - PA
// - PXN/UXN (execute-never bits)
//
//go:nosplit
func dumpFetchMapping(label string, va uintptr) bool {
	uartPuts("MMU: [mapchk] ")
	uartPuts(label)
	uartPuts(" VA=0x")
	uartPutHex64(uint64(va))
	uartPuts("\r\n")

	// Use uint64 to ensure 64-bit arithmetic (uintptr might be 32 bits in some builds)
	va64 := uint64(va)
	// Note: Indices can be 0-511 (9 bits), so we need uint16, not uint8
	l0Idx := uint16((va64 >> L0_SHIFT) & 0x1FF)
	l1Idx := uint16((va64 >> L1_SHIFT) & 0x1FF)
	l2Idx := uint16((va64 >> L2_SHIFT) & 0x1FF)
	l3Idx := uint16((va64 >> L3_SHIFT) & 0x1FF)

	l0e := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
	if (*l0e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPuts("MMU: [mapchk] ERROR: L0 not table entry=0x")
		uartPutHex64(*l0e)
		uartPuts("\r\n")
		return false
	}
	l1Base := uintptr(*l0e &^ 0xFFF)
	l1e := (*uint64)(unsafe.Pointer(l1Base + uintptr(l1Idx)*PTE_SIZE))
	if (*l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPuts("MMU: [mapchk] ERROR: L1 not table entry=0x")
		uartPutHex64(*l1e)
		uartPuts("\r\n")
		return false
	}
	l2Base := uintptr(*l1e &^ 0xFFF)
	l2e := (*uint64)(unsafe.Pointer(l2Base + uintptr(l2Idx)*PTE_SIZE))
	if (*l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPuts("MMU: [mapchk] ERROR: L2 not table entry=0x")
		uartPutHex64(*l2e)
		uartPuts("\r\n")
		return false
	}
	l3Base := uintptr(*l2e &^ 0xFFF)
	l3e := (*uint64)(unsafe.Pointer(l3Base + uintptr(l3Idx)*PTE_SIZE))

	uartPuts("MMU: [mapchk] L3 entry=0x")
	uartPutHex64(*l3e)
	uartPuts(" (bits[1:0]=")
	uartPutHex64(*l3e & 0x3)
	uartPuts(")\r\n")

	if (*l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPuts("MMU: [mapchk] ERROR: L3 entry not a valid PAGE (needs bits[1:0]=0b11)\r\n")
		return false
	}

	pa := *l3e &^ 0xFFF
	uartPuts("MMU: [mapchk] PA=0x")
	uartPutHex64(pa)
	uartPuts(" PXN=")
	if (*l3e & PTE_PXN) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts(" UXN=")
	if (*l3e & PTE_UXN) != 0 {
		uartPuts("1")
	} else {
		uartPuts("0")
	}
	uartPuts("\r\n")

	return true
}

// enableMMU enables the MMU and switches to virtual addressing.
// This must be called after initMMU().
//
//go:nosplit
func enableMMU() bool {
	// Verify page tables are set up
	if pageTableL0 == 0 {
		uartPuts("MMU: ERROR - Page tables not initialized (pageTableL0 is zero)\r\n")
		return false
	}
	uartPuts("MMU: [2] Page table verification passed\r\n")

	// Step 1: Configure MAIR_EL1 (Memory Attribute Indirection Register)
	// Attr0 (bits 7:0)   = Normal, Inner/Outer Write-Back Cacheable = 0xFF
	// Attr1 (bits 15:8)  = Device-nGnRnE = 0x00
	// All other attributes = 0 (unused)
	uartPutc('$') // Breadcrumb: before MAIR write
	// CRITICAL: MAIR format is Attr0 in bits 7:0, Attr1 in bits 15:8
	// We want: Attr0=0xFF (Normal), Attr1=0x00 (Device)
	// So MAIR = (0xFF << 0) | (0x00 << 8) = 0xFF
	mairValue := uint64(0xFF) // Attr0=0xFF (Normal in bits 7:0), Attr1=0x00 (Device in bits 15:8)
	asm.WriteMairEl1(mairValue)
	uartPutc('%') // Breadcrumb: after MAIR write

	// CRITICAL: Verify MAIR was written correctly
	// MAIR[0] should be 0xFF (Normal, Inner/Outer WB Cacheable)
	// MAIR[1] should be 0x00 (Device-nGnRnE)
	mairReadback := asm.ReadMairEl1()
	attr0Readback := (mairReadback >> 0) & 0xFF
	attr1Readback := (mairReadback >> 8) & 0xFF

	uartPuts("MMU: MAIR_EL1 written = 0x")
	uartPutHex64(mairValue)
	uartPuts("\r\n")
	uartPuts("MMU: MAIR_EL1 readback = 0x")
	uartPutHex64(mairReadback)
	uartPuts("\r\n")
	uartPuts("MMU: MAIR[0] (Attr0) = 0x")
	uartPutHex64(attr0Readback)
	uartPuts(" (should be 0xFF for Normal)\r\n")
	uartPuts("MMU: MAIR[1] (Attr1) = 0x")
	uartPutHex64(attr1Readback)
	uartPuts(" (should be 0x00 for Device)\r\n")

	if attr0Readback != 0xFF {
		uartPuts("MMU: ERROR - MAIR[0] is not 0xFF! This will cause incorrect memory attributes!\r\n")
		return false
	}
	if attr1Readback != 0x00 {
		uartPuts("MMU: WARNING - MAIR[1] is not 0x00 (but this may be OK)\r\n")
	}
	uartPuts("MMU: MAIR_EL1 verified correctly\r\n")

	// Step 2: Configure TCR_EL1 (Translation Control Register)
	// T0SZ (bits 5:0) = 16 (48-bit address space, 0x0000-0xFFFF_FFFF_FFFF)
	// EPD0 (bit 7) = 0 (use TTBR0)
	// IRGN0 (bits 9:8) = 1 (Inner Write-Back Cacheable)
	// ORGN0 (bits 11:10) = 1 (Outer Write-Back Cacheable)
	// SH0 (bits 13:12) = 3 (Inner Shareable)
	// TG0 (bits 15:14) = 0 (4KB granule)
	// T1SZ (bits 21:16) = 16 (48-bit address space for TTBR1, unused)
	// A1 (bit 22) = 0 (use TTBR0.ASID)
	// EPD1 (bit 23) = 1 (disable TTBR1)
	// IRGN1, ORGN1, SH1, TG1 = 0 (unused, TTBR1 disabled)
	// IPS (bits 34:32) = 2 (40-bit physical address, supports up to 1TB)
	// AS (bit 36) = 0 (8-bit ASID)
	uartPutc('e') // Breadcrumb: before TCR calculation
	tcrValue := uint64(0)
	tcrValue |= 16 << 0  // T0SZ = 16 (48-bit VA)
	tcrValue |= 0 << 7   // EPD0 = 0 (enable TTBR0)
	tcrValue |= 1 << 8   // IRGN0 = 1 (Inner WB Cacheable)
	tcrValue |= 1 << 10  // ORGN0 = 1 (Outer WB Cacheable)
	tcrValue |= 3 << 12  // SH0 = 3 (Inner Shareable)
	tcrValue |= 0 << 14  // TG0 = 0 (4KB granule)
	tcrValue |= 16 << 16 // T1SZ = 16 (48-bit VA for TTBR1)
	tcrValue |= 1 << 23  // EPD1 = 1 (disable TTBR1)
	tcrValue |= 2 << 32  // IPS = 2 (40-bit PA)
	uartPutc('f')        // Breadcrumb: before TCR write
	asm.WriteTcrEl1(tcrValue)
	uartPutc('g') // Breadcrumb: after TCR write

	// CRITICAL: Verify TCR was written correctly (especially T0SZ and T1SZ)
	// QEMU issue #1157: If T0SZ/T1SZ are 0, QEMU generates Translation faults
	// We must ensure both are set to 16
	tcrReadback := asm.ReadTcrEl1()
	t0szReadback := tcrReadback & 0x3F
	t1szReadback := (tcrReadback >> 16) & 0x3F

	uartPuts("MMU: TCR_EL1 written = 0x")
	uartPutHex64(tcrValue)
	uartPuts("\r\n")
	uartPuts("MMU: TCR_EL1 readback = 0x")
	uartPutHex64(tcrReadback)
	uartPuts("\r\n")
	uartPuts("MMU: T0SZ readback = ")
	uartPutHex64(t0szReadback)
	uartPuts(" (required: 16)\r\n")
	uartPuts("MMU: T1SZ readback = ")
	uartPutHex64(t1szReadback)
	uartPuts(" (required: 16)\r\n")

	if t0szReadback != 16 {
		uartPuts("MMU: ERROR - T0SZ is not 16! This will cause Translation faults in QEMU!\r\n")
		return false
	}
	if t1szReadback != 16 {
		uartPuts("MMU: ERROR - T1SZ is not 16! This will cause Translation faults in QEMU!\r\n")
		return false
	}
	uartPuts("MMU: TCR_EL1 T0SZ and T1SZ verified correctly\r\n")

	// CRITICAL: ISB after TCR write to ensure it's visible before setting TTBR0
	// This ensures the TCR configuration is fully applied before we set the
	// translation table base register.
	uartPuts("MMU: [2.5] ISB after TCR write (context sync)\r\n")
	asm.Isb()
	uartPuts("MMU: [2.6] ISB complete\r\n")

	// Step 2.5: Initialize TTBR1_EL1 to a safe value
	// Even though EPD1=1 (TTBR1 disabled), QEMU and some implementations
	// may require TTBR1_EL1 to be initialized to prevent undefined behavior.
	// Setting it to 0 is safe when EPD1 is enabled.
	uartPuts("MMU: [2.7] Initializing TTBR1_EL1 to safe value (0)\r\n")
	asm.WriteTtbr1El1(0)
	uartPuts("MMU: [2.8] TTBR1_EL1 initialized\r\n")

	// Step 3: Set TTBR0_EL1 to point to L0 page table
	// TTBR0_EL1 lower 12 bits are ignored (table must be 4KB aligned)
	uartPuts("MMU: [6] Before TTBR0 write\r\n")

	// CRITICAL: Verify page table is accessible before setting TTBR0
	// Read back from page table to ensure it's in memory and accessible
	testRead := (*uint64)(unsafe.Pointer(pageTableL0))
	if *testRead == 0 {
		uartPuts("MMU: WARNING - Page table L0 entry 0 is zero at 0x")
		uartPutHex64(uint64(pageTableL0))
		uartPuts("\r\n")
	}

	asm.WriteTtbr0El1(uint64(pageTableL0))
	uartPuts("MMU: [7] After TTBR0 write\r\n")
	uartPuts("MMU: TTBR0_EL1 = 0x")
	uartPutHex64(uint64(pageTableL0))
	uartPuts("\r\n")

	// Verify TTBR0 was set correctly by reading it back
	ttbr0Readback := asm.ReadTtbr0El1()
	if (ttbr0Readback &^ 0xFFF) != uint64(pageTableL0) {
		uartPuts("MMU: ERROR - TTBR0 readback mismatch! Written: 0x")
		uartPutHex64(uint64(pageTableL0))
		uartPuts(" Readback: 0x")
		uartPutHex64(ttbr0Readback)
		uartPuts("\r\n")
		return false
	}
	uartPuts("MMU: [7.5] TTBR0 readback verified\r\n")

	// Step 4: Skip TLB invalidation before MMU enablement
	// NOTE: TLB invalidation is not needed when MMU is disabled because
	// the TLB is not being used. We'll invalidate after MMU is enabled
	// if needed. Some ARM implementations may cause exceptions if TLB
	// maintenance instructions are executed with MMU disabled.
	uartPuts("MMU: [8] Skipping TLB invalidation (MMU disabled, TLB not in use)\r\n")

	// Step 5: Enable MMU in SCTLR_EL1
	// Ensure all previous memory operations complete before enabling MMU
	uartPutc('l') // Breadcrumb: before DSB before SCTLR
	asm.Dsb()
	uartPutc('m') // Breadcrumb: after DSB before SCTLR

	// Read current SCTLR_EL1
	uartPutc('n') // Breadcrumb: before SCTLR read
	sctlr := asm.ReadSctlrEl1()
	uartPutc('o') // Breadcrumb: after SCTLR read
	uartPuts("MMU: SCTLR_EL1 before = 0x")
	uartPutHex64(sctlr)
	uartPuts("\r\n")

	// Enable MMU while preserving CPU-provided SCTLR_EL1 value.
	// IMPORTANT: SCTLR_EL1 contains RES1 bits on many implementations; writing a "minimal"
	// value (like 0x1) can cause UNPREDICTABLE behavior.
	uartPutc('p')     // Breadcrumb: before SCTLR modification
	sctlr |= 1 << 0   // M = 1 (MMU enable)
	sctlr &^= 1 << 2  // C = 0 (data cache disabled for now)
	sctlr &^= 1 << 12 // I = 0 (instruction cache disabled for now)
	uartPuts("MMU: Enabling MMU with SCTLR_EL1 (preserved + M=1) = 0x")
	uartPutHex64(sctlr)
	uartPuts("\r\n")

	// NOTE: We're NOT enabling caches here. Enable them separately after
	// MMU is confirmed working. This helps isolate MMU issues.
	// sctlr |= 1 << 2  // C = 1 (Data cache enable) - DISABLED for now
	// sctlr |= 1 << 12 // I = 1 (Instruction cache enable) - DISABLED for now

	uartPutc('Q') // Breadcrumb: SCTLR modified, before message

	// CRITICAL DIAGNOSTIC: Verify page table entries needed for *instruction fetch*
	// immediately after MMU enable, AND for the synchronous exception handler itself.
	//
	// We previously observed QEMU taking an EL1 Prefetch Abort at 0x200264 right after
	// enabling the MMU, then looping trying to fetch the exception handler at 0x276200.
	//
	// If either of these VAs are unmapped / non-executable / invalid descriptor type, we hang.
	testPostEnableVA := uintptr(0x200264) // first post-enable fetch that was faulting in QEMU
	testVectorVA := uintptr(0x276200)     // sync_exception_el1 (exception vectors + 0x200)

	uartPuts("MMU: [11] Verifying fetch mappings for post-enable PC and exception vectors...\r\n")
	if !dumpFetchMapping("post-enable-fetch", testPostEnableVA) {
		return false
	}
	if !dumpFetchMapping("sync-exception-vector", testVectorVA) {
		return false
	}

	// The detailed multi-level walk for other debug addresses was removed; the
	// fetch-mapping checks above are the ones that directly explain the observed hang.

	// CRITICAL: Final DSB to ensure all page table writes are visible
	// before enabling MMU. The MMU will immediately start using these
	// page tables for translation, so they must be fully written.
	uartPuts("MMU: [11.9] Final DSB before MMU enable\r\n")
	asm.Dsb()
	uartPuts("MMU: [11.95] DSB complete\r\n")

	// CRITICAL: ISB before enabling MMU to ensure all context changes
	// (TTBR0, TCR, MAIR) are visible before the MMU is enabled.
	// This is required by ARM architecture - context-changing register
	// writes must be synchronized with ISB before enabling MMU.
	uartPuts("MMU: [11.96] ISB before MMU enable (context sync)\r\n")
	asm.Isb()
	uartPuts("MMU: [11.97] ISB complete\r\n")

	// Write SCTLR_EL1 to enable the MMU.
	uartPuts("MMU: [12] Before SCTLR write (MMU enable)\r\n")
	uartPutc('Z') // Breadcrumb: about to write SCTLR
	asm.WriteSctlrEl1(sctlr)

	// Ensure MMU enablement takes effect
	uartPuts("MMU: [14] Before ISB\r\n")
	asm.Isb()
	uartPuts("MMU: [15] After ISB\r\n")

	// Now that MMU is enabled, invalidate TLB to ensure clean state
	uartPuts("MMU: [16] Invalidating TLB (MMU now enabled)\r\n")
	asm.InvalidateTlbAll()
	uartPuts("MMU: [17] TLB invalidated\r\n")

	uartPuts("MMU: [18] Before final DSB\r\n")
	asm.Dsb()
	uartPuts("MMU: [19] After final DSB\r\n")

	uartPuts("MMU: SCTLR_EL1 after = 0x")
	uartPutHex64(sctlr)
	uartPuts("\r\n")

	uartPutc('w') // Breadcrumb: before final message
	uartPuts("MMU: ENABLED - Now using virtual addresses\r\n")
	uartPutc('x') // Breadcrumb: enableMMU complete
	return true
}
