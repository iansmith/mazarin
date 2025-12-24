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

	// Software-defined bits (bits 58-55, ignored by MMU hardware)
	// These bits can be used by the OS for page metadata/bookkeeping
	PTE_SW_LOCKED   = 1 << 55 // Page is locked, don't free
	PTE_SW_RESERVED = 1 << 56 // Page reserved for kernel use
	PTE_SW_KERNEL   = 1 << 57 // Kernel-owned page
	PTE_SW_USER     = 1 << 58 // User-accessible page

	// Execute permission flags
	PTE_EXEC_ALLOW = 0                  // PXN=0, UXN=0: Allow execution
	PTE_EXEC_NEVER = PTE_PXN | PTE_UXN  // PXN=1, UXN=1: Never execute

	// Memory attributes (bits 2-4, MAIR index)
	// MAIR[0] = Normal, Inner/Outer Write-Back Cacheable (0xFF)
	// MAIR[1] = Device-nGnRnE (0x00)
	// MAIR[2] = Normal, Inner/Outer Non-Cacheable (0x44)
	PTE_ATTR_NORMAL       = 0 << 2 // MAIR index 0
	PTE_ATTR_DEVICE       = 1 << 2 // MAIR index 1
	PTE_ATTR_NONCACHEABLE = 2 << 2 // MAIR index 2 - for page tables

	// Shareability (bits 8-9)
	PTE_SH_INNER = 3 << 8 // Inner shareable
	PTE_SH_OUTER = 2 << 8 // Outer shareable
	PTE_SH_NONE  = 0 << 8 // Non-shareable

	// Access permissions (bits 6-7)
	PTE_AP_RW     = 0 << 6 // Read/Write at EL0
	PTE_AP_RW_EL1 = 1 << 6 // Read/Write at EL1, no access at EL0
	PTE_AP_RO     = 2 << 6 // Read-only at EL0
	PTE_AP_RO_EL1 = 3 << 6 // Read-only at EL1, no access at EL0

	// Physical address mask for extracting PA from PTE
	// ARMv8-A: Output address is in bits 47:12 of the descriptor
	// Must mask out both lower bits (11:0) and upper attribute bits (63:48)
	PTE_ADDR_MASK = 0x0000FFFFFFFFF000
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
	// PHYSICAL MEMORY LAYOUT (256MB QEMU RAM: 0x40000000 - 0x50000000)
	//
	// Region                     Start        End          Size     Purpose
	// --------------------------------------------------------------------------------
	// Mazboot Executable        0x40000000 - 0x41FFFFFF   32 MB    Code/data/BSS/stack
	// Page Table Pool           0x42000000 - 0x427FFFFF    8 MB    L1/L2/L3 page tables
	//                                                              for demand paging
	// Kmazarin Physical Frames  0x42800000 - 0x50000000  216 MB    Physical frame pool
	//                                                              (demand-allocated)
	//
	PAGE_TABLE_POOL_START = 0x42000000 // Start of page table pool (8MB)
	PAGE_TABLE_POOL_END   = 0x42800000 // End of page table pool

	PHYS_FRAME_BASE = 0x42800000 // Start of physical frame pool (after page tables)
	PHYS_FRAME_END  = 0x50000000 // End (216MB pool, within 256MB QEMU RAM)

	// Virtual mmap region (large virtual, demand-paged)
	// VA range is large but physical backing is limited by PAGE_LIMIT
	//
	// Go runtime arm64 hints start at 0x4000000000 (256GB) and go up.
	// Formula: p = uintptr(i)<<40 | 0x4000000000 for i in [0, 0x7f]
	// Max with formula: 0x7F0004000000 ≈ 8.4 PB
	// ARMv8-A supports 48-bit VA = 256TB max
	//
	// CRITICAL: Kmazarin runtime uses very high stack addresses (seen 279TB)
	// Accept up to 1PB to handle all reasonable Go runtime addresses
	MMAP_VIRT_BASE = 0x48000000       // Start of virtual mmap region (our bump allocator)
	MMAP_VIRT_END  = 0x4000000000000  // End of virtual mmap region (1PB)

	// Memory limits
	MAX_KERNEL_PAGES = 262144         // 1GB / 4KB = 262,144 pages max
	MAX_KERNEL_BYTES = 1 << 30        // 1GB

	// kmalloc heap: Fixed region for kernel heap allocator (UART buffers, etc.)
	// Placed at a fixed address to avoid conflicts with:
	//   - Go's BSS (ends at ~0x40147000)
	//   - Page metadata array (starts at __end, ~768KB for 128MB RAM)
	//   - Go's runtime heap (uses demand paging at 0x4000000000+)
	//
	// Memory layout:
	//   0x40147000: __end (BSS ends)
	//   0x40147000-0x40247000: Page metadata array (~1MB reserved)
	//   0x48000000-0x4C000000: kmalloc heap (64MB, this region)
	//   0x5E000000-0x60000000: MMU page tables
	//   0x5EFFFE000: g0 stack bottom
	KMALLOC_HEAP_BASE = 0x48000000              // Fixed start address for kmalloc heap
	KMALLOC_HEAP_SIZE = 64 * 1024 * 1024        // 64MB heap size
	KMALLOC_HEAP_END  = 0x4C000000              // End of kmalloc heap region
)

// MMIODevice describes an MMIO device region to be mapped
type MMIODevice struct {
	// name field removed to avoid write barrier in nosplit context
	start uintptr // Physical base address (from linker symbol)
	size  uintptr // Size in bytes
	attr  uint64  // Page table attributes (PTE_ATTR_*)
	ap    uint64  // Access permissions (PTE_AP_*)
}

// MMIO devices to map (initialized once in initMMU)
var mmioDevices [4]MMIODevice  // Fixed-size array to avoid heap allocation
var mmioDeviceCount int

// Page table structure
var (
	pageTableL0 uintptr   // Level 0 table (PGD)
	pageTableL1 uintptr   // Level 1 table (PUD)
	pageTableL2 []uintptr // Level 2 tables (PMD) - allocated as needed
	pageTableL3 []uintptr // Level 3 tables (PT) - allocated as needed

	// DEBUG: Counter to reduce L3 debug verbosity
	l3DebugCounter uint32

	// DEBUG: Counter for page faults during demand paging
	pageFaultCounter uint32

	// DEBUG: Counter to detect exception loops
	totalExceptionCounter uint32
	lastExceptionVA uintptr
	sameVACounter uint32
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
		// Fatal error - use direct UART to avoid stack depth
		uartPutcDirect('P')
		uartPutcDirect('T')
		uartPutcDirect('A')
		uartPutcDirect('L')
		uartPutcDirect('I')
		uartPutcDirect('G')
		uartPutcDirect('N')
		uartPutcDirect('!')
		for {
		} // Halt on alignment error
	}

	// Check for overflow (ensure we don't exceed allocated region)
	if alloc.offset+TABLE_SIZE > PAGE_TABLE_SIZE {
		// Fatal error - use direct UART to avoid stack depth
		uartPutcDirect('P')
		uartPutcDirect('T')
		uartPutcDirect('O')
		uartPutcDirect('V')
		uartPutcDirect('E')
		uartPutcDirect('R')
		uartPutcDirect('!')
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
	// 	uartPutcDirect('P')  // Breadcrumb: entered initPhysFrameAllocator - DISABLED
	alloc := getPhysFrameAllocator()
	// 	uartPutcDirect('p')  // Breadcrumb: got allocator - DISABLED
	alloc.next = PHYS_FRAME_BASE
	alloc.end = PHYS_FRAME_END
	alloc.pagesAlloc = 0

	// Calculate pre-mapped pages
	// Mazboot (32MB) + Page Table Pool (8MB) = 40MB pre-mapped
	// Kmazarin frames are demand-allocated, not pre-mapped
	preMappedBytes := uintptr(PHYS_FRAME_BASE - 0x40000000) // 40MB pre-mapped (mazboot + page tables)
	preMappedPages := uint32(preMappedBytes / PAGE_SIZE)
	setTotalKernelPages(preMappedPages)

	poolSize := PHYS_FRAME_END - PHYS_FRAME_BASE
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

// GetPageFaultCounter returns the current page fault count
// This allows exceptions.go to access the counter for debugging
//go:nosplit
func GetPageFaultCounter() uint32 {
	return pageFaultCounter
}

// preMapPages pre-maps specific pages that are known to cause issues
// This is a workaround to test if demand paging at certain addresses causes hangs
//go:nosplit
func preMapPages() {
	// Pre-map the 64KB boundary page that causes fault #17 to hang
	// VA 0x4000010000 is exactly at a 64KB boundary
	const targetVA = uintptr(0x4000010000)

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		print("ERROR: Failed to allocate physical frame for pre-mapping\r\n")
		return
	}

	// Zero the physical frame (it's identity-mapped, so we can access it directly)
	bzero(unsafe.Pointer(physFrame), PAGE_SIZE)

	// Map the page
	mapPage(targetVA, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Ensure the mapping is visible
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()

	print("Pre-mapped VA 0x4000010000 -> PA 0x")
	uartPutHex64(uint64(physFrame))
	print("\r\n")
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
	// CRITICAL: Do NOT access global variables here!
	// Global variables might not be mapped yet and would cause nested exceptions
	// Commented out to prevent nested exception from unmapped globals:
	//   totalExceptionCounter, lastExceptionVA, sameVACounter, pageFaultCounter

	// Track total exceptions to detect loops
	// totalExceptionCounter++  // DISABLED: causes nested exception

	// Detect exception loops (same VA faulting repeatedly)
	// DISABLED: causes nested exception from accessing unmapped globals
	// if faultAddr == lastExceptionVA {
	// 	sameVACounter++
	// 	if sameVACounter > 3 {
	// 		uartPutsDirect("\r\n!EXCEPTION LOOP! VA=0x")
	// 		uartPutHex64Direct(uint64(faultAddr))
	// 		uartPutsDirect(" count=")
	// 		uartPutHex64Direct(uint64(sameVACounter))
	// 		uartPutsDirect("\r\n")
	// 		for {} // Hang
	// 	}
	// } else {
	// 	sameVACounter = 1
	// 	lastExceptionVA = faultAddr
	// }

	// PERFORMANCE: Minimize debug output for fast demand paging
	// Only print progress dots every 100 faults, not full debug info
	// pageFaultCounter++  // DISABLED: causes nested exception

	// DEBUG: Print page fault address
	// DISABLED: Even this might cause issues
	// uartPutsDirect("\r\nPF VA=0x")
	// uartPutHex64Direct(uint64(faultAddr))

	// CRITICAL: Validate that the fault address is in a registered mmap span
	//
	// This is our security boundary - only addresses that were explicitly mmap'd
	// are eligible for demand paging. This automatically rejects:
	// - NULL pointers (0x0)
	// - ROM/Flash region (0x0-0x8000000)
	// - MMIO devices (0x8000000-0x40000000)
	// - Unmapped high addresses (>1PB)
	// - Any other region that wasn't explicitly requested via mmap
	//
	if !isInMmapSpan(faultAddr) {
		uartPutsDirect("\r\n!PAGE FAULT at unmapped address: VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		uartPutsDirect("Not in any mmap span. Possible causes:\r\n")
		uartPutsDirect("  - NULL pointer dereference\r\n")
		uartPutsDirect("  - ROM/Flash access (not supported)\r\n")
		uartPutsDirect("  - MMIO access (use direct MMIO functions)\r\n")
		uartPutsDirect("  - Access to memory not allocated via mmap\r\n")
		return false
	}

	// faultAddr is now validated to be in a legitimate mmap'd region

	// Align fault address to page boundary
	pageAddr := faultAddr &^ (PAGE_SIZE - 1)

	// uartPutsDirect(" check...")  // DISABLED

	// CHECK: Is this page already mapped?
	// This shouldn't happen - page faults should only occur for unmapped pages
	// But if it does happen, we want to detect it
	existingPA := getPhysicalAddress(pageAddr)

	// uartPutsDirect(" exist=0x")  // DISABLED
	// uartPutHex64Direct(uint64(existingPA))  // DISABLED

	if existingPA != 0 {
		// DEBUG: Get the actual PTE to see what flags are set
		va64 := uint64(pageAddr)
		l0Idx := uint16((va64 >> 39) & 0x1FF)
		l1Idx := uint16((va64 >> 30) & 0x1FF)
		l2Idx := uint16((va64 >> 21) & 0x1FF)
		l3Idx := uint16((va64 >> 12) & 0x1FF)

		l0Entry := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
		l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
		l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))
		l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)
		l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))
		l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)
		l3Entry := (*uint64)(unsafe.Pointer(l3Table + uintptr(l3Idx)*PTE_SIZE))

		uartPutsDirect("\r\n!DUPLICATE FAULT at VA=0x")
		uartPutHex64Direct(uint64(pageAddr))
		uartPutsDirect(" PA=0x")
		uartPutHex64Direct(uint64(existingPA))
		uartPutsDirect(" PTE=0x")
		uartPutHex64Direct(*l3Entry)
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(faultStatus)
		uartPutsDirect("\r\n")

		// CRITICAL FIX: Flush TLB for this address!
		// The page is already mapped in page tables, but TLB has stale entry
		// Use full TLB invalidation instead of VA-specific, as VA-specific
		// flush may not work correctly for all address ranges
		asm.Dsb()                  // Ensure all memory operations complete
		asm.InvalidateTlbAll()     // Invalidate ALL TLBs (nuclear option)
		asm.Dsb()                  // Ensure TLB invalidation completes
		asm.Isb()                  // Synchronize context

		// This is already mapped - return success without allocating
		return true
	}

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		// Out of physical memory - this is fatal for demand paging
		uartPutsDirect("\r\nDEMAND PAGE OOM at VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		return false
	}

	// uartPutsDirect(" PA=0x")  // DISABLED
	// uartPutHex64Direct(uint64(physFrame))  // DISABLED

	// CRITICAL: Verify physical frame is in valid range and not in page table region
	if physFrame < PHYS_FRAME_BASE || physFrame >= PHYS_FRAME_END {
		uartPutsDirect("\r\n!INVALID PHYS FRAME: 0x")
		uartPutHex64Direct(uint64(physFrame))
		uartPutsDirect("\r\n")
		for {} // Hang
	}
	if physFrame >= PAGE_TABLE_BASE && physFrame < PAGE_TABLE_END {
		uartPutsDirect("\r\n!PHYS FRAME IN PAGE TABLE REGION: 0x")
		uartPutHex64Direct(uint64(physFrame))
		uartPutsDirect("\r\n")
		for {} // Hang
	}

	// uartPutsDirect(" map...")  // DISABLED

	// CRITICAL: Map the page FIRST, then zero via VA (not PA)
	// We cannot safely access the physical frame directly because it might not
	// be identity-mapped. After creating the VA→PA mapping, we can safely zero
	// via the virtual address.
	mapPage(pageAddr, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Ensure page table writes are visible before TLB flush
	asm.Dsb()

	// PERFORMANCE: Invalidate TLB only for this specific VA, not entire TLB
	// This keeps the TLB warm for other addresses, dramatically improving performance
	asm.InvalidateTlbVa(pageAddr)

	// Ensure TLB invalidation completes before returning
	asm.Isb()

	// uartPutsDirect(" bzero...")  // DISABLED

	// NOW zero the page via the VA (not PA!)
	// After mapPage() and TLB invalidation, the VA is accessible and mapped to physFrame
	// SECURITY: Always zero new pages to prevent leaking old data
	bzero(unsafe.Pointer(pageAddr), PAGE_SIZE)

	// DEBUG: Print completion for ALL faults to track success
	// uartPutsDirect(" OK")  // DISABLED

	return true
}

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
	// CRITICAL: Use Non-Shareable for now to debug MMU enable issues
	entry := uint64(addr) | PTE_VALID | PTE_TABLE | PTE_AF | attrs | ap | exec | PTE_SH_NONE
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
func mapPage(va, pa uintptr, attrs uint64, ap uint64, exec uint64) {
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
		if l1Table == 0 {
			uartPutsDirect("\r\n!L1 ALLOC FAILED!\r\n")
			return
		}
		*l0Entry = createTableEntry(l1Table)
		asm.Dsb() // Ensure table entry is visible
		// Note: TLB invalidation deferred to end of mapPage
	}

	// Extract L1 table address from L0 entry
	l1Table := uintptr(*l0Entry & PTE_ADDR_MASK) // Extract PA from PTE (bits 47:12)

	// Update global pageTableL1 for consistency (though we don't use it in this function)
	pageTableL1 = l1Table

	// Get L1 entry
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + uintptr(l1Idx)*PTE_SIZE))

	// If L1 entry doesn't point to L2 table, create it
	var l2Table uintptr
	if (*l1Entry & PTE_TABLE) == 0 {
		l2Table = allocatePageTable()
		if l2Table == 0 {
			uartPutsDirect("\r\n!L2 ALLOC FAILED!\r\n")
			return
		}

		*l1Entry = createTableEntry(l2Table)
		asm.Dsb() // Ensure table entry is visible
		// Note: TLB invalidation deferred to end of mapPage
	} else {
		l2Table = uintptr(*l1Entry & PTE_ADDR_MASK)
	}

	// Get L2 entry
	l2Entry := (*uint64)(unsafe.Pointer(l2Table + uintptr(l2Idx)*PTE_SIZE))

	// LAZY ALLOCATION: If L2 entry doesn't point to L3 table, create it now
	// This is the key optimization - we only allocate L3 tables when needed
	var l3Table uintptr
	if (*l2Entry & PTE_TABLE) == 0 {
		l3Table = allocatePageTable() // Allocate L3 table on-demand
		if l3Table == 0 {
			uartPutsDirect("\r\n!L3 ALLOC FAILED!\r\n")
			return
		}

		*l2Entry = createTableEntry(l3Table)
		asm.Dsb() // Ensure table entry is visible
		// Note: TLB invalidation deferred to end of mapPage
	} else {
		l3Table = uintptr(*l2Entry & PTE_ADDR_MASK)
	}

	// Set L3 entry (the actual page)
	l3EntryAddr := l3Table + uintptr(l3Idx)*PTE_SIZE
	l3Entry := (*uint64)(unsafe.Pointer(l3EntryAddr))

	pteValue := createPageTableEntry(pa, attrs, ap, exec)
	*l3Entry = pteValue

	// DEBUG: For .text section first page, print the PTE value
	if va == 0x40100000 {  // Updated for new kernel load address
		uartPutsDirect("\nDEBUG .text first page: VA=0x")
		uartPutHex64Direct(uint64(va))
		uartPutsDirect(" PA=0x")
		uartPutHex64Direct(uint64(pa))
		uartPutsDirect(" exec=0x")
		uartPutHex64Direct(exec)
		uartPutsDirect(" PTE=0x")
		uartPutHex64Direct(pteValue)
		uartPutsDirect("\n")
	}

	// DEBUG: Output 'M' for every page we map in the .rodata range
	if va >= 0x3DE000 && va < 0x3F3000 {
	// 		uartPutcDirect('M') - DISABLED
	}

	asm.CleanDcacheVa(l3EntryAddr) // Ensure PTE write is visible to page table walker

	// CRITICAL: Ensure page table writes are visible before continuing
	// Use DSB to ensure all page table writes complete before any subsequent
	// memory access or MMU operation
	asm.Dsb()

	// Invalidate TLB for high-memory VAs only (>4GB)
	// Low memory (<4GB) is identity-mapped and doesn't need TLB flush during init
	// This avoids issues with early boot when globals might not be mapped yet
	const HIGH_MEMORY_THRESHOLD = uintptr(0x100000000) // 4GB
	if va >= HIGH_MEMORY_THRESHOLD {
		asm.InvalidateTlbAll()
		asm.Isb()
	}

	// Additional verification: read back the entry to ensure it was written
	// This helps catch any memory ordering or cache coherency issues
	verifyEntry := *l3Entry
	if verifyEntry != *l3Entry {
		// This shouldn't happen, but if it does, it indicates a serious issue
		print("MMU: WARNING - Page table entry readback mismatch!\r\n")
	}
}

// mapRegion maps a contiguous region of memory
// vaStart: Start virtual address (must be 4KB aligned)
// vaEnd: End virtual address (exclusive, must be 4KB aligned)
// paStart: Start physical address (must be 4KB aligned)
// attrs: Memory attributes
// ap: Access permissions
// exec: Execute permissions
//
//go:nosplit
func mapRegion(vaStart, vaEnd, paStart uintptr, attrs uint64, ap uint64, exec uint64) {
	// Sanity check - detect invalid ranges
	if vaStart >= vaEnd || (vaEnd - vaStart) > 0x100000000 {
		return
	}

	va := vaStart
	pa := paStart

	for va < vaEnd {
		mapPage(va, pa, attrs, ap, exec)
		va += PAGE_SIZE
		pa += PAGE_SIZE
	}

	asm.Dsb()
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
	// Allocate page table memory
	pageTableL0 = PAGE_TABLE_BASE
	pageTableL1 = PAGE_TABLE_BASE + TABLE_SIZE

	// Initialize the bump allocator after the pre-allocated L0 + L1 tables
	ptAlloc := getPageTableAllocator()
	ptAlloc.base = PAGE_TABLE_BASE
	ptAlloc.offset = TABLE_SIZE * 2

	// Verify page table base is 4KB aligned
	if pageTableL0&0xFFF != 0 {
		print("FATAL: Page table base not 4KB aligned\r\n")
		return false
	}

	// Zero out page tables
	bzero(unsafe.Pointer(pageTableL0), TABLE_SIZE*2)

	// Set up L0 table to point to L1 table for identity mapping
	l0Entry0 := (*uint64)(unsafe.Pointer(pageTableL0 + 0*PTE_SIZE))
	*l0Entry0 = createTableEntry(pageTableL1)

	// Map low memory regions with correct permissions
	// CRITICAL FIX: Data section MUST be read-write!
	//
	// Memory layout:
	//   0x000000-0x56D000: Boot code, text, rodata (read-only)
	//   0x56D000-0x632000: Data section (READ-WRITE - was causing permission faults!)
	//
	// CRITICAL: Map .rodata explicitly FIRST to ensure string constants are accessible
	// We can safely call getLinkerSymbol() here because MMU is not yet enabled -
	// we're still using physical addressing, so .rodata is directly accessible
	// 	uartPutcDirect('R')  // Breadcrumb: about to map .rodata - DISABLED
	rodataStart := getLinkerSymbol("__rodata_start")
	rodataEnd := getLinkerSymbol("__rodata_end")
	if rodataStart != 0 && rodataEnd != 0 {
	// 		uartPutcDirect('r')  // Breadcrumb: got .rodata - DISABLED addresses, about to map
		mapRegion(rodataStart, rodataEnd, rodataStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	// 		uartPutcDirect('D')  // Breadcrumb: .rodata mapped - DISABLED
	} else {
	// 		uartPutcDirect('X')  // Breadcrumb: getLinkerSymbol failed - DISABLED!
	}

	// Get section boundaries from linker symbols
	textStart := getLinkerSymbol("__text_start")
	dataStart := getLinkerSymbol("__data_start")
	endAddr := getLinkerSymbol("__end")

	// DEBUG: Print text section range to verify it includes executing code
	uartPutsDirect("\r\n.text: 0x")
	uartPutHex64Direct(uint64(textStart))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(rodataStart))
	uartPutsDirect("\r\n")

	// Map everything before .rodata as read-only (boot code, text)
	// This includes:
	// - .text section (code)
	// - .vectors section (exception handlers)
	if textStart > 0 && rodataStart > 0 && textStart < rodataStart {
		uartPutsDirect(".text: 0x")
		uartPutHex64Direct(uint64(textStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(rodataStart))
		uartPutsDirect(" AP=0x")
		uartPutHex64Direct(PTE_AP_RO_EL1)
		uartPutsDirect(" EXEC_ALLOW=0x")
		uartPutHex64Direct(PTE_EXEC_ALLOW)
		uartPutsDirect(" EXEC_NEVER=0x")
		uartPutHex64Direct(PTE_EXEC_NEVER)
		uartPutsDirect("\r\n")
		mapRegion(textStart, rodataStart, textStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_ALLOW)
	// 		uartPutcDirect('T')  // .text mapped - DISABLED

	}

	// Map everything after .rodata up to data section as read-only
	if rodataEnd > 0 && dataStart > 0 && rodataEnd < dataStart {
		mapRegion(rodataEnd, dataStart, rodataEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Map data+BSS sections as read-write
	// BSS starts where data ends, so we map from dataStart to __bss_end
	// This includes both .data (initialized) and .bss (uninitialized) sections
	bssEnd := getLinkerSymbol("__bss_end")
	if dataStart > 0 && bssEnd > 0 {
		mapRegion(dataStart, bssEnd, dataStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// Map remainder after BSS up to end of kernel image as read-only (if there's anything)
	if bssEnd > 0 && endAddr > 0 && bssEnd < endAddr {
		mapRegion(bssEnd, endAddr, bssEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Initialize MMIO devices array (fixed-size to avoid heap allocation)
	// 	uartPutcDirect('I')  // Breadcrumb: about to init MMIO - DISABLED devices array

	// CRITICAL: Call assembly functions directly instead of getLinkerSymbol()
	// getLinkerSymbol() uses string comparison which accesses misaligned .rodata
	// Before MMU is enabled, ARM64 requires strict alignment, so we must avoid
	// string access. The assembly functions return linker symbols without string ops.

	// Device 0: GIC (Generic Interrupt Controller)
	mmioDevices[0] = MMIODevice{
		start: asm.GetGicBase(),
		size:  asm.GetGicSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 1: UART PL011
	mmioDevices[1] = MMIODevice{
		start: asm.GetUartBase(),
		size:  asm.GetUartSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 2: QEMU fw_cfg
	mmioDevices[2] = MMIODevice{
		start: asm.GetFwcfgBase(),
		size:  asm.GetFwcfgSize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	// Device 3: bochs-display framebuffer
	mmioDevices[3] = MMIODevice{
		start: asm.GetBochsDisplayBase(),
		size:  asm.GetBochsDisplaySize(),
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	mmioDeviceCount = 4

	// Map all MMIO devices
	for i := 0; i < mmioDeviceCount; i++ {
		dev := &mmioDevices[i]
		mapRegion(dev.start, dev.start+dev.size, dev.start, dev.attr, dev.ap, PTE_EXEC_NEVER)
	}

	// Map DTB region (now that kernel starts at 0x40100000, no overlap!)
	dtbStart := getLinkerSymbol("__dtb_boot_addr")
	dtbEnd := dtbStart + getLinkerSymbol("__dtb_size")
	mapRegion(
		dtbStart, dtbEnd, dtbStart,
		PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER,  // DTB is read-only data
	)
	// 	uartPutcDirect('B')  // Breadcrumb: DTB region mapped - DISABLED

	// Map PCI ECAM (lowmem and highmem)
	// 	uartPutcDirect('E')  // Breadcrumb: about to map ECAM - DISABLED
	ecamBase := uintptr(0x3F000000)
	ecamSize := uintptr(0x01000000) // 16MB, not 256MB!
	mapRegion(ecamBase, ecamBase+ecamSize, ecamBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	// 	uartPutcDirect('e')  // Breadcrumb: lowmem ECAM mapped - DISABLED

	highmemEcamBase := uintptr(0x4010000000)
	highmemEcamSize := uintptr(0x10000000)
	mapRegion(highmemEcamBase, highmemEcamBase+highmemEcamSize, highmemEcamBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	// 	uartPutcDirect('h')  // Breadcrumb: highmem ECAM mapped - DISABLED

	// Verify lowmem mapping (silent unless error)
	// TEMPORARILY DISABLED: dumpFetchMapping() uses string parameters which access .rodata
	// dumpFetchMapping("pci-ecam-low", ecamBase)
	// 	uartPutcDirect('V')  // Breadcrumb: verification done - DISABLED

	// Map kernel RAM (after mazboot image to page tables) - heap, stacks
	// CRITICAL: Start mapping AFTER our kernel image (endAddr) to avoid overlap
	// 	uartPutcDirect('0')  // Breadcrumb: about to map RAM - DISABLED
	ramStart := (endAddr + 0xFFF) &^ 0xFFF  // Round up to next page
	uartPutsDirect("endAddr=0x")
	uartPutHex64Direct(uint64(endAddr))
	uartPutsDirect(" ramStart=0x")
	uartPutHex64Direct(uint64(ramStart))
	uartPutsDirect(" PAGE_TABLE_BASE=0x")
	uartPutHex64Direct(uint64(PAGE_TABLE_BASE))
	uartPutsDirect("\r\n")
	// 	uartPutcDirect('1')  // Breadcrumb: got RAM start - DISABLED address
	// Pre-map heap region as RW, non-executable
	// NOTE: Kmazarin segments will overlap with this region, but will be remapped later
	// with correct permissions (some executable). The remapping is allowed to update permissions.
	if ramStart < PAGE_TABLE_BASE {
		uartPutsDirect("Mapping RAM: 0x")
		uartPutHex64Direct(uint64(ramStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(PAGE_TABLE_BASE))
		uartPutsDirect(" (RW)\r\n")
		mapRegion(ramStart, PAGE_TABLE_BASE, ramStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	// 		uartPutcDirect('2')  // Breadcrumb: RAM mapped - DISABLED
	}

	// CRITICAL: Map exception vector RAM region as NORMAL CACHEABLE and EXECUTABLE
	// Exception vectors will be relocated to 0x41100000 from ROM
	// Must be cacheable for instruction fetch (CPUs cannot execute from non-cacheable memory)
	// Cache coherency is ensured by cleaning instruction cache after copying vectors
	const EXCEPTION_VECTOR_RAM_START = uintptr(0x41100000)
	const EXCEPTION_VECTOR_RAM_END = uintptr(0x41101000) // 4KB (2KB needed, rounded up)
	mapRegion(EXCEPTION_VECTOR_RAM_START, EXCEPTION_VECTOR_RAM_END,
		EXCEPTION_VECTOR_RAM_START, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_ALLOW)

	// PERFORMANCE: Map page table region (0x5E000000 - 0x60000000) as CACHEABLE
	// ARM64's hardware page table walker is cache-coherent - it will see cached updates.
	// Using Normal Cacheable memory dramatically improves performance by avoiding slow
	// memory accesses on every page table walk.
	// We use proper barriers (DSB ISH) after PTE modifications and TLB invalidation
	// to ensure coherency between CPU data cache and page table walker.
	// NOTE: This region includes the stack guard area (0x5EFD0000-0x5F000000),
	// which is now cacheable along with the page tables.
	mapRegion(PAGE_TABLE_BASE, PAGE_TABLE_END, PAGE_TABLE_BASE, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// NOTE: Physical frame pool (0x50000000-0x5E000000) is already mapped
	// as part of kernel RAM above (0x40100000-0x5E000000), so no separate mapping needed

	// Initialize physical frame allocator
	initPhysFrameAllocator()
	// 	uartPutcDirect('F')  // Breadcrumb: physical frame allocator - DISABLED initialized

	// CRITICAL: Flush TLB to ensure all mappings are visible to CPU
	// Without this, the CPU may have stale TLB entries that don't reflect
	// the new mappings, causing exceptions when accessing newly mapped regions
	asm.Dsb()               // Ensure all page table writes are visible
	asm.InvalidateTlbAll()  // Invalidate all TLB entries
	asm.Isb()               // Ensure TLB invalidation completes

	// DEBUG: Check if we ran out of page table space
	_, remaining := getPageTableAllocatorStats()
	if remaining == 0 {
	// 		uartPutcDirect('O')  // 'O' = Out of page table - DISABLED space!
		for {} // Hang
	}

	// 	uartPutcDirect('T')  // Breadcrumb: about to return true - DISABLED from initMMU
	return true
}

// preMapScheديnitPages pre-maps the 22 pages that would normally cause page faults
// during runtime.schedinit(). This is a debugging aid to isolate whether the
// problem is with the 22nd exception itself, or with something after 22 exceptions.
//
//go:nosplit
func preMapScheديnitPages() {
	// Get UART base for progress markers
	uartBase := getLinkerSymbol("__uart_base")

	// Print marker to show we're pre-mapping
	uartPutsDirect("\r\nPre-mapping 22 schedinit pages...")

	// These are the exact addresses that cause page faults during schedinit,
	// captured from a previous run. We'll pre-map them to avoid the faults.
	faultAddrs := [22]uintptr{
		0x00000000D1280000,
		0x00000000D1288000,
		0x00000000D3292000,
		0x0000000091300000,
		0x000000008D290000,
		0x000000008CA82000,
		0x000000008C980400,
		0x000000008C960080,
		0x00000000B1300000,
		0x00000000D3392000,
		0x00000000D3280000,
		0x00000000D3291008,
		0x00000000D33A2000,
		0x00000000D3290000,
		0x0000004000001F80,
		0x0000004000000000,
		0x0000004000003F80,
		0x0000004000002000,
		0x0000004000004000,
		0x000000400000FF80,
		0x000000400000E000,
		0x0000004000010000,
	}

	for i := 0; i < len(faultAddrs); i++ {
		addr := faultAddrs[i]

		// Align to page boundary
		pageAddr := addr &^ (PAGE_SIZE - 1)

		// Allocate physical frame
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			uartPutsDirect("\r\nOOM during pre-mapping!\r\n")
			for {} // Hang
		}

		// Zero the frame
		bzero(unsafe.Pointer(physFrame), PAGE_SIZE)

		// Map it
		mapPage(pageAddr, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

		// Print progress marker every 5 pages
		if (i+1) % 5 == 0 {
			*(*uint32)(unsafe.Pointer(uartBase)) = 0x2E  // '.'
		}
	}

	// Flush TLB after all mappings
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()

	uartPutsDirect(" done!\r\n")
}

// dumpFetchMapping verifies the L3 mapping for a virtual address (silent unless error)
//
//go:nosplit
func dumpFetchMapping(label string, va uintptr) bool {
	_ = label // unused unless error

	va64 := uint64(va)
	l0Idx := uint16((va64 >> L0_SHIFT) & 0x1FF)
	l1Idx := uint16((va64 >> L1_SHIFT) & 0x1FF)
	l2Idx := uint16((va64 >> L2_SHIFT) & 0x1FF)
	l3Idx := uint16((va64 >> L3_SHIFT) & 0x1FF)

	l0e := (*uint64)(unsafe.Pointer(pageTableL0 + uintptr(l0Idx)*PTE_SIZE))
	if (*l0e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		print("MMU: L0 mapping error\r\n")
		return false
	}
	l1Base := uintptr(*l0e & PTE_ADDR_MASK)
	l1e := (*uint64)(unsafe.Pointer(l1Base + uintptr(l1Idx)*PTE_SIZE))
	if (*l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		print("MMU: L1 mapping error\r\n")
		return false
	}
	l2Base := uintptr(*l1e & PTE_ADDR_MASK)
	l2e := (*uint64)(unsafe.Pointer(l2Base + uintptr(l2Idx)*PTE_SIZE))
	if (*l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		print("MMU: L2 mapping error\r\n")
		return false
	}
	l3Base := uintptr(*l2e & PTE_ADDR_MASK)
	l3e := (*uint64)(unsafe.Pointer(l3Base + uintptr(l3Idx)*PTE_SIZE))

	if (*l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		print("MMU: L3 mapping error\r\n")
		return false
	}

	return true
}

// enableMMU enables the MMU and switches to virtual addressing.
//
//go:nosplit
func enableMMU() bool {
	// uartBase := uintptr(0x09000000) // BREADCRUMB DISABLED
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x45 // 'E' = entered enableMMU - BREADCRUMB DISABLED

	if pageTableL0 == 0 {
		print("FATAL: Page tables not initialized\r\n")
		return false
	}
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x31 // '1' = page table check passed - BREADCRUMB DISABLED

	// Configure MAIR_EL1: Set all 3 memory attribute indices
	// MAIR[0] = 0xFF (Normal, Inner/Outer Write-Back Cacheable)
	// MAIR[1] = 0x00 (Device-nGnRnE)
	// MAIR[2] = 0x44 (Normal, Inner/Outer Non-Cacheable)
	mairValue := uint64(0xFF) |      // Attr0: Normal cacheable
		(uint64(0x00) << 8) |  // Attr1: Device
		(uint64(0x44) << 16)   // Attr2: Normal non-cacheable
	asm.WriteMairEl1(mairValue)
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x32 // '2' = MAIR written - BREADCRUMB DISABLED

	// Verify MAIR
	mairReadback := asm.ReadMairEl1()
	if (mairReadback & 0xFFFFFF) != mairValue {
		print("FATAL: MAIR configuration failed\r\n")
		return false
	}
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x33 // '3' = MAIR verified - BREADCRUMB DISABLED

	// Configure TCR_EL1
	tcrValue := uint64(0)
	tcrValue |= 16 << 0  // T0SZ = 16
	tcrValue |= 1 << 8   // IRGN0 = 1
	tcrValue |= 1 << 10  // ORGN0 = 1
	tcrValue |= 3 << 12  // SH0 = 3
	tcrValue |= 16 << 16 // T1SZ = 16
	tcrValue |= 1 << 23  // EPD1 = 1
	tcrValue |= 2 << 32  // IPS = 2
	asm.WriteTcrEl1(tcrValue)
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x34 // '4' = TCR written - BREADCRUMB DISABLED

	// Verify TCR
	tcrReadback := asm.ReadTcrEl1()
	if (tcrReadback & 0x3F) != 16 {
		print("FATAL: TCR T0SZ configuration failed\r\n")
		return false
	}
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x35 // '5' = TCR verified - BREADCRUMB DISABLED

	asm.Isb()
	asm.WriteTtbr1El1(0)
	asm.WriteTtbr0El1(uint64(pageTableL0))
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x36 // '6' = TTBR written - BREADCRUMB DISABLED

	// Verify TTBR0
	ttbr0Readback := asm.ReadTtbr0El1()
	if (ttbr0Readback &^ 0xFFF) != uint64(pageTableL0) {
		print("FATAL: TTBR0 configuration failed\r\n")
		return false
	}
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x37 // '7' = TTBR verified - BREADCRUMB DISABLED

	asm.Dsb()

	// Read and modify SCTLR_EL1
	sctlr := asm.ReadSctlrEl1()
	sctlr |= 1 << 0   // M = 1 (MMU enable)
	sctlr &^= 1 << 2  // C = 0 (data cache disabled)
	sctlr &^= 1 << 12 // I = 0 (instruction cache disabled)

	// DEBUG: Print TTBR0 and page table base to verify setup - DISABLED
	// uartPutsDirect("\r\nTTBR0=0x")
	// uartPutHex64Direct(asm.ReadTtbr0El1())
	// uartPutsDirect(" PTBase=0x")
	// uartPutHex64Direct(uint64(pageTableL0))
	// uartPutsDirect("\r\n")

	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x38 // '8' = About to enable MMU

	// DEBUG: Manually walk page table for exception handler address - DISABLED
	// {
	// 	testVA := uintptr(0x40161200)
	// 	uartPutsDirect("Check VA=0x")
	// 	uartPutHex64Direct(uint64(testVA))
	// 	uartPutsDirect("\r\n")
	//
	// 	l0Idx := (testVA >> 39) & 0x1FF
	// 	l1Idx := (testVA >> 30) & 0x1FF
	// 	l2Idx := (testVA >> 21) & 0x1FF
	// 	l3Idx := (testVA >> 12) & 0x1FF
	//
	// 	uartPutsDirect("L0[")
	// 	uartPutHex64Direct(uint64(l0Idx))
	// 	uartPutsDirect("] L1[")
	// 	uartPutHex64Direct(uint64(l1Idx))
	// 	uartPutsDirect("] L2[")
	// 	uartPutHex64Direct(uint64(l2Idx))
	// 	uartPutsDirect("] L3[")
	// 	uartPutHex64Direct(uint64(l3Idx))
	// 	uartPutsDirect("]\r\n")
	//
	// 	l0Table := (*[512]uint64)(unsafe.Pointer(pageTableL0))
	// 	l0Entry := l0Table[l0Idx]
	// 	uartPutsDirect("L0[")
	// 	uartPutHex64Direct(uint64(l0Idx))
	// 	uartPutsDirect("]=0x")
	// 	uartPutHex64Direct(l0Entry)
	// 	uartPutsDirect("\r\n")
	//
	// 	if (l0Entry & 0x3) != 0x3 {
	// 		uartPutsDirect("L0 NOT TABLE!\r\n")
	// 	} else {
	// 		l1Base := l0Entry & PTE_ADDR_MASK
	// 		l1Table := (*[512]uint64)(unsafe.Pointer(uintptr(l1Base)))
	// 		l1Entry := l1Table[l1Idx]
	// 		uartPutsDirect("L1[")
	// 		uartPutHex64Direct(uint64(l1Idx))
	// 		uartPutsDirect("]=0x")
	// 		uartPutHex64Direct(l1Entry)
	// 		uartPutsDirect("\r\n")
	//
	// 		if (l1Entry & 0x3) != 0x3 {
	// 			uartPutsDirect("L1 NOT TABLE!\r\n")
	// 		} else {
	// 			l2Base := l1Entry & PTE_ADDR_MASK
	// 			l2Table := (*[512]uint64)(unsafe.Pointer(uintptr(l2Base)))
	// 			l2Entry := l2Table[l2Idx]
	// 			uartPutsDirect("L2[")
	// 			uartPutHex64Direct(uint64(l2Idx))
	// 			uartPutsDirect("]=0x")
	// 			uartPutHex64Direct(l2Entry)
	// 			uartPutsDirect("\r\n")
	//
	// 			if (l2Entry & 0x3) != 0x3 {
	// 				uartPutsDirect("L2 NOT TABLE!\r\n")
	// 			} else {
	// 				l3Base := l2Entry & PTE_ADDR_MASK
	// 				l3Table := (*[512]uint64)(unsafe.Pointer(uintptr(l3Base)))
	// 				l3Entry := l3Table[l3Idx]
	// 				uartPutsDirect("L3[")
	// 				uartPutHex64Direct(uint64(l3Idx))
	// 				uartPutsDirect("]=0x")
	// 				uartPutHex64Direct(l3Entry)
	// 				uartPutsDirect("\r\n")
	//
	// 				if (l3Entry & 0x1) == 0 {
	// 					uartPutsDirect("L3 NOT VALID - THIS IS THE BUG!\r\n")
	// 				} else {
	// 					physAddr := l3Entry & PTE_ADDR_MASK
	// 					uartPutsDirect("L3 OK, PA=0x")
	// 					uartPutHex64Direct(physAddr)
	// 					uartPutsDirect("\r\n")
	// 				}
	// 			}
	// 		}
	// 	}
	// }

	// CRITICAL: Cannot call dumpFetchMapping() before MMU is enabled!
	// dumpFetchMapping uses print() which accesses .rodata strings, and before
	// MMU is on, ARM64 requires strict alignment (strings are misaligned).
	// TODO: Add verification AFTER MMU enable when unaligned access is allowed.

	// DEBUG: Check L3 entry for the next instruction after MMU enable - DISABLED
	// {
	// 	testVA := uintptr(0x401003b8) // NOP after msr SCTLR_EL1 (updated for new kernel address)
	// 	l3Idx := (testVA >> 12) & 0x1FF
	//
	// 	l0Idx := (testVA >> 39) & 0x1FF
	// 	l1Idx := (testVA >> 30) & 0x1FF
	// 	l2Idx := (testVA >> 21) & 0x1FF
	//
	// 	l0Table := (*[512]uint64)(unsafe.Pointer(pageTableL0))
	// 	l0Entry := l0Table[l0Idx]
	// 	l1Table := (*[512]uint64)(unsafe.Pointer(uintptr(l0Entry & PTE_ADDR_MASK)))
	// 	l1Entry := l1Table[l1Idx]
	// 	l2Table := (*[512]uint64)(unsafe.Pointer(uintptr(l1Entry & PTE_ADDR_MASK)))
	// 	l2Entry := l2Table[l2Idx]
	// 	l3Table := (*[512]uint64)(unsafe.Pointer(uintptr(l2Entry & PTE_ADDR_MASK)))
	// 	l3Entry := l3Table[l3Idx]
	//
	// 	uartPutsDirect("L3[0x")
	// 	uartPutHex64Direct(uint64(l3Idx))
	// 	uartPutsDirect("]=0x")
	// 	uartPutHex64Direct(l3Entry)
	// 	uartPutsDirect("\r\n")
	// }

	// Invalidate TLB before enabling MMU to prevent stale translations
	asm.InvalidateTlbAll()
	asm.Dsb()

	// Enable MMU
	asm.Dsb()
	asm.Isb()
	asm.WriteSctlrEl1(sctlr)
	asm.Isb()
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x39 // '9' = MMU enabled - DISABLED

	// Now MMU is ON and unaligned access is allowed, can safely use print()
	// *(*uint32)(unsafe.Pointer(uartBase)) = 0x4D // 'M' = MMU enabled successfully - DISABLED

	return true
}
