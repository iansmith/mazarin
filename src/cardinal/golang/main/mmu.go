package main

import (
	"unsafe"

	"cardinal/asm"
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
	// PHYSICAL MEMORY LAYOUT (256MB QEMU RAM: 0x40000000 - 0x50000000)
	//
	// All addresses calculated from linker.ld - SINGLE SOURCE OF TRUTH
	//
	// Region                     Start        End          Size     Purpose
	// --------------------------------------------------------------------------------
	// DTB                       0x40000000 - 0x40100000    1 MB    Device Tree Blob
	// Cardinal Executable       0x40100000 - 0x41000000   15 MB    Code/data/BSS/heap
	// Page Tables               0x41000000 - 0x41800000    8 MB    L0/L1/L2/L3 tables
	//                                                              (from linker.ld)
	// Kmazarin Executable       0x41800000 - ~0x41A00000  ~2 MB    ELF segments
	//                                                              (loaded at runtime)
	// Physical Frame Pool       ~0x41A00000 - 0x50000000 ~230 MB   Demand paging pool
	//                                                              (exact start varies)
	//
	// Page tables are identity-mapped at addresses from linker.ld:
	//   __page_tables_start = 0x41000000 (from linker.ld PAGE_TABLE_START)
	//   __page_tables_end   = 0x41800000 (from linker.ld PAGE_TABLE_END)
	//
	// NOTE: Do NOT hardcode page table addresses here. Use getLinkerSymbol() to
	// retrieve __page_tables_start and __page_tables_end at runtime.

	// Physical frame pool end (256 MB limit)
	PHYS_FRAME_END  = 0x50000000 // End of 256MB RAM (BOOT_ADDRESS + 256MB)

	// Stack sizes (must be page-aligned multiples)
	STACK_SIZE_G0_CARDINAL     = 64 * 1024   // 64KB for g0 stack (normal execution)
	STACK_SIZE_EXCEPTION_EL1   = 128 * 1024  // 128KB for exception handler stack (increased from 64KB to fix overflow)

	// Stack base address (nice round number, placed after frame pool)
	// This is the TOP of the exception stack (highest address)
	// Stacks grow downward from here
	STACK_BASE = 0x5F020000  // Round number ending in 0000 (increased from 0x5F010000)

	// Kmazarin conservative size estimate (for initial PHYS_FRAME_BASE calculation)
	// Actual kmazarin size is determined after ELF load; this is a safe upper bound
	// Typical kmazarin is 1-3 MB, we reserve 8 MB to be safe
	KMAZARIN_CONSERVATIVE_SIZE = 8 * 1024 * 1024 // 8 MB

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
	//   Page tables and heap are calculated dynamically at runtime - see initKmallocHeap()
)

// Kmalloc heap boundaries - calculated dynamically from linker symbols
// DO NOT initialize these with hardcoded values - they are set by initKmallocHeap()
var (
	KMALLOC_HEAP_BASE uintptr // Start of kmalloc heap (page-aligned after BSS)
	KMALLOC_HEAP_SIZE uintptr // Size of kmalloc heap in bytes
	KMALLOC_HEAP_END  uintptr // End of kmalloc heap (= __cardinal_end)
)

// Page table size - read from linker symbol
var PAGE_TABLE_SIZE uintptr

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

// kernelPanic prints a panic message and halts the system
// Uses direct UART writes to work before UART initialization
//
//go:nosplit
func kernelPanic(msg string) {
	uartBase := uintptr(0x09000000)

	// Write "Kernel Panic: "
	panicMsg := "Kernel Panic: "
	for i := 0; i < len(panicMsg); i++ {
		*(*uint32)(unsafe.Pointer(uartBase)) = uint32(panicMsg[i])
	}

	// Write the actual message
	for i := 0; i < len(msg); i++ {
		*(*uint32)(unsafe.Pointer(uartBase)) = uint32(msg[i])
	}

	// Write newline
	*(*uint32)(unsafe.Pointer(uartBase)) = '\r'
	*(*uint32)(unsafe.Pointer(uartBase)) = '\n'

	asm.SemihostingExit()
	for {} // Infinite loop if semihosting doesn't work
}

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
		return
	}

	// Zero the physical frame (it's identity-mapped, so we can access it directly)
	bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

	// Map the page
	mapPage(targetVA, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Ensure the mapping is visible
	asm.Dsb()
	asm.InvalidateTlbAll()
	asm.Isb()
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
// NOTE: nosplit removed - called from syncExceptionDispatchInternal on g0 stack.
//
//go:noinline
func HandlePageFault(faultAddr uintptr, faultStatus uint64) bool {
	// ==========================================================================
	// CRITICAL: NESTED PAGE FAULT DETECTION
	// ==========================================================================
	// If we're already in a page fault handler, we have a nested fault.
	// This is FATAL - it means the page fault handler itself is accessing
	// unmapped memory, which would cause infinite recursion.
	//
	// The inPageFaultHandler_global variable MUST be in BSS (pre-mapped before
	// MMU enable) so that reading/writing it cannot itself trigger a page fault.
	//
	if inPageFaultHandler_global != 0 {
		// NESTED PAGE FAULT DETECTED - FATAL ERROR
		uartPutsDirect("\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("! FATAL: NESTED PAGE FAULT DETECTED                         !\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("Nested fault address: VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		uartPutsDirect("Fault status: 0x")
		uartPutHex64Direct(faultStatus)
		uartPutsDirect("\r\n")
		uartPutsDirect("\r\n")
		uartPutsDirect("This means the page fault handler accessed unmapped memory.\r\n")
		uartPutsDirect("Likely causes:\r\n")
		uartPutsDirect("  1. A global variable used by HandlePageFault is not pre-mapped\r\n")
		uartPutsDirect("  2. physFrameAllocatorState_global not in identity-mapped BSS\r\n")
		uartPutsDirect("  3. mmapSpans array not in identity-mapped BSS\r\n")
		uartPutsDirect("  4. pageTableL0 pointer not accessible\r\n")
		uartPutsDirect("\r\n")
		uartPutsDirect("Run VerifyCriticalGlobalsMapped() at KernelMain start to debug.\r\n")
		uartPutsDirect("\r\n")
		// Print a simple backtrace using saved registers
		uartPutsDirect("Halting immediately.\r\n")
		for {
			// Infinite loop - halt the system
		}
	}

	// Mark that we're now inside the page fault handler
	inPageFaultHandler_global = 1

	// DEBUG: Print simple breadcrumb to show demand paging is working
	// Also print the actual fault address for low address faults (more interesting)
	if faultAddr >= 0x4000000000 {
		uartPutcDirect('*') // High address page fault
	} else {
		// For low addresses, print the fault address
		uartPutcDirect('[')
		uartPutHex64Direct(uint64(faultAddr))
		uartPutcDirect(']')
	}

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
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
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
		uartPutsDirect(" IFSC=0x")
		uartPutHex64Direct(faultStatus)
		// Print full ESR by reading it directly
		esr := asm.ReadEsrEl1()
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(esr)
		// Print ELR to see what address we're returning to
		elr := asm.ReadElrEl1()
		uartPutsDirect(" ELR=0x")
		uartPutHex64Direct(elr)
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
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
		return true
	}

	// Allocate a physical frame
	physFrame := allocPhysFrame()
	if physFrame == 0 {
		// Out of physical memory - this is fatal for demand paging
		uartPutsDirect("\r\nDEMAND PAGE OOM at VA=0x")
		uartPutHex64Direct(uint64(faultAddr))
		uartPutsDirect("\r\n")
		inPageFaultHandler_global = 0 // Clear re-entrancy guard before return
		return false
	}

	// uartPutsDirect(" PA=0x")  // DISABLED
	// uartPutHex64Direct(uint64(physFrame))  // DISABLED

	// CRITICAL: Verify physical frame is in valid range
	if physFrame >= PHYS_FRAME_END {
		uartPutsDirect("\r\n!INVALID PHYS FRAME (beyond 256MB): 0x")
		uartPutHex64Direct(uint64(physFrame))
		uartPutsDirect(" end=0x")
		uartPutHex64Direct(uint64(PHYS_FRAME_END))
		uartPutsDirect("\r\n")
		for {} // Hang
	}

	// Verify frame is not in page table region
	// This runs after MMU is enabled, so string access is OK, but use direct calls for consistency
	pageTableStart := asm.GetPageTablesStartAddr()
	pageTableEnd := asm.GetPageTablesEndAddr()
	if physFrame >= pageTableStart && physFrame < pageTableEnd {
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
	bzero4K(unsafe.Pointer(pageAddr), PAGE_SIZE)

	// CRITICAL: Data Synchronization Barrier after zeroing the page
	// Without this barrier, the zero writes may still be in the CPU's store buffer
	// when we return from the exception. The retried instruction (e.g., writing
	// span.startAddr) would execute, but subsequent reads could see stale data
	// (the buffered zeroes) if the store buffer hasn't drained.
	//
	// DSB ensures all memory writes (including the bzero) are visible to all
	// observers before any subsequent instructions execute.
	asm.Dsb()

	// DEBUG: Print completion for ALL faults to track success
	// uartPutsDirect(" OK")  // DISABLED

	// Clear re-entrancy guard - page fault handled successfully
	inPageFaultHandler_global = 0
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

	// DEBUG: Print a dot every 100 pages for first few
	mapPageDebugCount++
	if mapPageDebugCount <= 5 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '.'
	}

	// Extract level indices from virtual address
	// Use uint64 to ensure 64-bit arithmetic (uintptr might be 32 bits in some builds)
	va64 := uint64(va)

	// Use explicit shift values to avoid any constant folding issues
	// Note: Indices can be 0-511 (9 bits), so we need uint16, not uint8
	l0Idx := uint16((va64 >> 39) & 0x1FF) // Bits 48-39
	l1Idx := uint16((va64 >> 30) & 0x1FF) // Bits 38-30
	l2Idx := uint16((va64 >> 21) & 0x1FF) // Bits 29-21
	l3Idx := uint16((va64 >> 12) & 0x1FF) // Bits 20-12

	doDebug := mapPageDebugCount <= 3

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

	pageNum := 0
	for va < vaEndAligned {
		if pageNum%8 == 0 {
			uartPutcDirect('.') // Progress dot every 8 pages
		}
		mapPageInitMMU(va, pa, attrs, ap, exec)
		va += PAGE_SIZE
		pa += PAGE_SIZE
		pageNum++
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

// initMMU initializes the MMU with identity-mapped page tables
// This must be called before enabling the MMU
// Returns true on success, false on failure
//
// NOTE: Not nosplit - called from KernelMain which allows stack growth
func initMMU() bool {
	// Early breadcrumb - write directly to UART to avoid any function call issues
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = '1'

	// Initialize cache line size for optimized bzero
	initCacheLineSize()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'C' // Cache line init done

	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata
	// which isn't mapped yet when initMMU() is called!
	pageTableBase := asm.GetPageTablesStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = '2'
	pageTableEnd := asm.GetPageTablesEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = '3'

	// Calculate PAGE_TABLE_SIZE from the difference
	PAGE_TABLE_SIZE = pageTableEnd - pageTableBase
	*(*uint32)(unsafe.Pointer(uartBase)) = '4'

	// Allocate page table memory
	pageTableL0 = pageTableBase
	pageTableL1 = pageTableBase + TABLE_SIZE
	*(*uint32)(unsafe.Pointer(uartBase)) = '5'

	// Initialize the bump allocator after the pre-allocated L0 + L1 tables
	ptAlloc := getPageTableAllocator()
	*(*uint32)(unsafe.Pointer(uartBase)) = '6'
	ptAlloc.base = pageTableBase
	ptAlloc.offset = TABLE_SIZE * 2
	*(*uint32)(unsafe.Pointer(uartBase)) = '7'

	// Verify page table base is 4KB aligned
	if pageTableL0&0xFFF != 0 {
		// Fatal error - use direct UART (character by character to avoid .rodata access)
		uartPutcDirect('P')
		uartPutcDirect('T')
		uartPutcDirect('0')
		uartPutcDirect('!')
		uartPutcDirect(' ')
		uartPutHex64Direct(uint64(pageTableL0))
		uartPutcDirect('\r')
		uartPutcDirect('\n')
		for {
		} // Halt
	}

	// Zero out page tables (L0 + L1, each 4KB = 8KB total)
	*(*uint32)(unsafe.Pointer(uartBase)) = '8'
	uartPutHex64Direct(uint64(pageTableL0))
	*(*uint32)(unsafe.Pointer(uartBase)) = ':'
	bzero4K(unsafe.Pointer(pageTableL0), TABLE_SIZE)
	bzero4K(unsafe.Pointer(pageTableL1), TABLE_SIZE)
	*(*uint32)(unsafe.Pointer(uartBase)) = '9'

	// Set up L0 table to point to L1 table for identity mapping
	l0Entry0 := (*uint64)(unsafe.Pointer(pageTableL0 + 0*PTE_SIZE))
	*l0Entry0 = createTableEntry(pageTableL1)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'a'

	// Map low memory regions with correct permissions
	// CRITICAL FIX: Data section MUST be read-write!
	//
	// Memory layout:
	//   0x000000-0x56D000: Boot code, text, rodata (read-only)
	//   0x56D000-0x632000: Data section (READ-WRITE - was causing permission faults!)
	//
	// CRITICAL: Call assembly helpers directly instead of getLinkerSymbol()
	// because getLinkerSymbol() uses string comparisons that access .rodata!
	*(*uint32)(unsafe.Pointer(uartBase)) = 'b'
	rodataStart := asm.GetRodataStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'c'
	rodataEnd := asm.GetRodataEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'd'
	if rodataStart != 0 && rodataEnd != 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '['
		mapRegionInitMMU(rodataStart, rodataEnd, rodataStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
		*(*uint32)(unsafe.Pointer(uartBase)) = ']'
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 'e'

	// Get section boundaries from linker symbols
	textStart := asm.GetTextStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'f'
	dataStart := asm.GetDataStartAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'g'
	endAddr := asm.GetEndAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'h'

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
		mapRegionInitMMU(textStart, rodataStart, textStart, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_ALLOW)
	}

	// Map everything after .rodata up to data section as read-only
	if rodataEnd > 0 && dataStart > 0 && rodataEnd < dataStart {
		mapRegionInitMMU(rodataEnd, dataStart, rodataEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Map data+BSS sections as read-write
	// BSS starts where data ends, so we map from dataStart to __bss_end
	// This includes both .data (initialized) and .bss (uninitialized) sections
	bssEnd := asm.GetBssEndAddr()
	if dataStart > 0 && bssEnd > 0 {
		uartPutsDirect("Mapping .data+.bss: 0x")
		uartPutHex64Direct(uint64(dataStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(bssEnd))
		uartPutsDirect("\r\n")
		mapRegionInitMMU(dataStart, bssEnd, dataStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		uartPutcDirect('i') // breadcrumb: .data+.bss mapped
	}

	// Map remainder after BSS up to end of kernel image as read-only (if there's anything)
	if bssEnd > 0 && endAddr > 0 && bssEnd < endAddr {
		mapRegionInitMMU(bssEnd, endAddr, bssEnd, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
	}

	// Initialize MMIO devices array (fixed-size to avoid heap allocation)
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
	// NOTE: bochs-display is 16MB - too large to map at boot
	// Will be mapped on-demand if/when display is used
	mmioDevices[3] = MMIODevice{
		start: asm.GetBochsDisplayBase(),
		size:  0, // Skip mapping for now - too slow
		attr:  PTE_ATTR_DEVICE,
		ap:    PTE_AP_RW_EL1,
	}
	mmioDeviceCount = 4
	uartPutcDirect('j') // breadcrumb: MMIO devices initialized

	// Map all MMIO devices
	for i := 0; i < mmioDeviceCount; i++ {
		dev := &mmioDevices[i]
		uartPutcDirect('0' + byte(i)) // which device
		uartPutHex64Direct(uint64(dev.start))
		uartPutcDirect(':')
		uartPutHex64Direct(uint64(dev.size))
		uartPutcDirect(' ')
		if dev.size > 0 {
			mapRegionInitMMU(dev.start, dev.start+dev.size, dev.start, dev.attr, dev.ap, PTE_EXEC_NEVER)
		}
		uartPutcDirect('!')
	}
	uartPutcDirect('k') // breadcrumb: MMIO devices mapped

	// Map DTB region (now that kernel starts at 0x40100000, no overlap!)
	dtbStart := asm.GetDtbBootAddr()
	dtbEnd := dtbStart + asm.GetDtbSize()
	mapRegionInitMMU(
		dtbStart, dtbEnd, dtbStart,
		PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER,  // DTB is read-only data
	)

	// Map PCI ECAM (lowmem only, minimal subset)
	// NOTE: Only map first 64KB (16 devices) to avoid slow page-by-page mapping
	// Full ECAM is 16MB (lowmem) + 256MB (highmem) = too slow
	ecamBase := uintptr(0x3F000000)
	ecamSize := uintptr(0x00010000) // Only 64KB for essential PCI access
	mapRegionInitMMU(ecamBase, ecamBase+ecamSize, ecamBase, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	uartPutcDirect('l') // breadcrumb: PCI ECAM mapped

	// SKIP highmem ECAM (256MB) - will map on-demand if needed
	// SKIP PCI BAR region (240MB) - will map on-demand if needed

	// Get page table region boundaries from linker.ld
	// Note: pageTableEnd already declared earlier in function
	pageTableEnd = asm.GetPageTablesEndAddr()

	// Map kernel RAM (after cardinal image to page tables) - heap, stacks
	// CRITICAL: Start mapping AFTER our kernel image (endAddr) to avoid overlap
	// We map from end of cardinal to start of page tables
	ramStart := (endAddr + 0xFFF) &^ 0xFFF  // Round up to next page
	uartPutsDirect("endAddr=0x")
	uartPutHex64Direct(uint64(endAddr))
	uartPutsDirect(" ramStart=0x")
	uartPutHex64Direct(uint64(ramStart))
	uartPutsDirect(" pageTableBase=0x")
	uartPutHex64Direct(uint64(pageTableBase))
	uartPutsDirect("\r\n")

	// Pre-map heap region (cardinal kmalloc heap) as RW, non-executable
	// This maps from end of cardinal sections to start of page tables
	// NOTE: This is a large region (~13MB) - takes a while to map page by page
	if ramStart < pageTableBase {
		uartPutsDirect("Mapping RAM: 0x")
		uartPutHex64Direct(uint64(ramStart))
		uartPutsDirect(" - 0x")
		uartPutHex64Direct(uint64(pageTableBase))
		uartPutsDirect(" (RW)\r\n")
		mapRegionInitMMU(ramStart, pageTableBase, ramStart, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		uartPutcDirect('m') // breadcrumb: RAM mapped
	}

	// PERFORMANCE: Map page table region as CACHEABLE
	// ARM64's hardware page table walker is cache-coherent - it will see cached updates.
	// Using Normal Cacheable memory dramatically improves performance by avoiding slow
	// memory accesses on every page table walk.
	// We use proper barriers (DSB ISH) after PTE modifications and TLB invalidation
	// to ensure coherency between CPU data cache and page table walker.
	// Page table region is from linker.ld: 0x41000000 - 0x41800000 (8MB)
	mapRegionInitMMU(pageTableBase, pageTableEnd, pageTableBase, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	uartPutcDirect('n') // breadcrumb: page tables mapped

	// NOTE: Exception vectors are now embedded in .text section at their final location
	// They no longer need a separate RAM mapping at 0x41100000 (which would conflict
	// with page tables). The vectors are mapped as part of the .text section mapping above.

	// NOTE: Physical frame pool starts after kmazarin executable and extends to PHYS_FRAME_END
	// The exact start address depends on kmazarin size (determined at runtime after ELF load)
	// This region will be identity-mapped as part of the general RAM mapping

	// NOTE: Bump allocator region (0x48000000 - 0xC8000000) is registered as a span
	// but NOT pre-mapped in page tables. Instead, pages are mapped on-demand via page
	// fault handler when kmazarin first accesses them. This saves ~512K page table entries!
	//
	// When kmazarin writes to unmapped address in bump region:
	//   1. Data abort exception occurs
	//   2. Exception handler checks if address is in registered span
	//   3. If yes: map the 4KB page on-demand (identity map VA=PA) and continue
	//   4. If no: real bug, panic with fault details

	// CRITICAL FIX: Map BOTH stacks - g0 stack and exception stack
	// Both operate at EL1 privilege level (no EL0 execution yet)
	//
	// Stack Architecture:
	// - g0 stack (SP_EL0): Used for normal kernel execution in EL1t mode (SPSel=0)
	// - Exception stack (SP_EL1): Used for exception handlers in EL1h mode (SPSel=1)
	//
	// Stack layout (grows downward from STACK_BASE):
	//   STACK_BASE (0x5F020000) ← SP_EL1 (exception stack top)
	//   ↓ exception stack (STACK_SIZE_EXCEPTION_EL1 = 128KB)
	//   0x5F000000 ← SP_EL0 (g0 stack top)
	//   ↓ g0 stack (STACK_SIZE_G0_CARDINAL = 64KB)
	//   0x5EFF0000 (g0 stack bottom)

	// Compute exception stack addresses from STACK_BASE
	exceptionStackTop := uintptr(STACK_BASE)
	exceptionStackBottom := exceptionStackTop - uintptr(STACK_SIZE_EXCEPTION_EL1)

	// Compute g0 stack addresses (grows downward from exception stack bottom)
	g0StackTop := exceptionStackBottom
	g0StackBottom := g0StackTop - uintptr(STACK_SIZE_G0_CARDINAL)

	// Map g0 stack (SP_EL0) - boot.s must set SP_EL0 to g0StackTop
	uartPutsDirect("Mapping g0 stack (SP_EL0): 0x")
	uartPutHex64Direct(uint64(g0StackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(g0StackTop))
	uartPutsDirect(" (RW)\r\n")
	mapRegionInitMMU(g0StackBottom, g0StackTop, g0StackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Map exception stack (SP_EL1) - boot.s must set SP_EL1 to exceptionStackTop
	uartPutsDirect("Mapping exception stack (SP_EL1): 0x")
	uartPutHex64Direct(uint64(exceptionStackBottom))
	uartPutsDirect(" - 0x")
	uartPutHex64Direct(uint64(exceptionStackTop))
	uartPutsDirect(" (RW)\r\n")
	mapRegionInitMMU(exceptionStackBottom, exceptionStackTop, exceptionStackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

	// Initialize physical frame allocator
	uartPutcDirect('C')
	initPhysFrameAllocator()
	uartPutcDirect('a')

	// CRITICAL: Clean data cache for page tables before enabling MMU
	// The hardware page table walker reads PTEs from memory, not cache.
	// If PTEs are still in data cache (write-back), walker can't see them!
	uartPutcDirect('r')
	pageTableSize := pageTableEnd - pageTableBase
	for offset := uintptr(0); offset < pageTableSize; offset += 64 {
		asm.CleanDataCacheVA(pageTableBase + offset)
	}
	uartPutcDirect('d')

	// CRITICAL: Flush TLB to ensure all mappings are visible to CPU
	// Without this, the CPU may have stale TLB entries that don't reflect
	// the new mappings, causing exceptions when accessing newly mapped regions
	asm.Dsb()               // Ensure all page table writes and cache cleans complete
	uartPutcDirect('i')
	asm.InvalidateTlbAll()  // Invalidate all TLB entries
	uartPutcDirect('n')
	asm.Isb()               // Ensure TLB invalidation completes
	uartPutcDirect('a')

	// DEBUG: Check if we ran out of page table space
	_, remaining := getPageTableAllocatorStats()
	uartPutcDirect('l')
	if remaining == 0 {
		uartPutcDirect('!')  // Out of page table space!
		for {} // Hang
	}

	return true
}

// preMapScheديnitPages pre-maps the 22 pages that would normally cause page faults
// during runtime.schedinit(). This is a debugging aid to isolate whether the
// problem is with the 22nd exception itself, or with something after 22 exceptions.
//
//go:nosplit
func preMapScheديnitPages() {
	// Get UART base for progress markers
	uartBase := asm.GetUartBase()

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
		bzero4K(unsafe.Pointer(physFrame), PAGE_SIZE)

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
		return false
	}
	l1Base := uintptr(*l0e & PTE_ADDR_MASK)
	l1e := (*uint64)(unsafe.Pointer(l1Base + uintptr(l1Idx)*PTE_SIZE))
	if (*l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}
	l2Base := uintptr(*l1e & PTE_ADDR_MASK)
	l2e := (*uint64)(unsafe.Pointer(l2Base + uintptr(l2Idx)*PTE_SIZE))
	if (*l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}
	l3Base := uintptr(*l2e & PTE_ADDR_MASK)
	l3e := (*uint64)(unsafe.Pointer(l3Base + uintptr(l3Idx)*PTE_SIZE))

	if (*l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		return false
	}

	return true
}

// dumpPageTableWalk dumps the complete page table walk for a virtual address
// showing all L0->L1->L2->L3 entries with their bit fields.
// This is a diagnostic function to verify page table structure.
//
//go:nosplit
func dumpPageTableWalk(label string, va uintptr) {
	uartPutsDirect("\r\n=== Page Table Walk for ")
	uartPutsDirect(label)
	uartPutsDirect(" ===\r\n")
	uartPutsDirect("VA: 0x")
	uartPutHex64Direct(uint64(va))
	uartPutsDirect("\r\n")

	va64 := uint64(va)
	l0Idx := uint16((va64 >> L0_SHIFT) & 0x1FF)
	l1Idx := uint16((va64 >> L1_SHIFT) & 0x1FF)
	l2Idx := uint16((va64 >> L2_SHIFT) & 0x1FF)
	l3Idx := uint16((va64 >> L3_SHIFT) & 0x1FF)

	uartPutsDirect("Indices: L0=")
	uartPutHex64Direct(uint64(l0Idx))
	uartPutsDirect(" L1=")
	uartPutHex64Direct(uint64(l1Idx))
	uartPutsDirect(" L2=")
	uartPutHex64Direct(uint64(l2Idx))
	uartPutsDirect(" L3=")
	uartPutHex64Direct(uint64(l3Idx))
	uartPutsDirect("\r\n")

	// L0 entry
	uartPutsDirect("L0 table base: 0x")
	uartPutHex64Direct(uint64(pageTableL0))
	uartPutsDirect("\r\n")

	l0EntryAddr := pageTableL0 + uintptr(l0Idx)*PTE_SIZE
	l0e := *(*uint64)(unsafe.Pointer(l0EntryAddr))
	uartPutsDirect("L0[")
	uartPutHex64Direct(uint64(l0Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l0EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l0e)
	dumpPTEBits(l0e, true) // true = table descriptor

	if (l0e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L0 entry! Bits[1:0]=")
		uartPutHex64Direct(l0e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L1 entry
	l1Base := uintptr(l0e & PTE_ADDR_MASK)
	l1EntryAddr := l1Base + uintptr(l1Idx)*PTE_SIZE
	l1e := *(*uint64)(unsafe.Pointer(l1EntryAddr))
	uartPutsDirect("L1[")
	uartPutHex64Direct(uint64(l1Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l1EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l1e)
	dumpPTEBits(l1e, true) // table descriptor

	if (l1e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L1 entry! Bits[1:0]=")
		uartPutHex64Direct(l1e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L2 entry
	l2Base := uintptr(l1e & PTE_ADDR_MASK)
	l2EntryAddr := l2Base + uintptr(l2Idx)*PTE_SIZE
	l2e := *(*uint64)(unsafe.Pointer(l2EntryAddr))
	uartPutsDirect("L2[")
	uartPutHex64Direct(uint64(l2Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l2EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l2e)
	dumpPTEBits(l2e, true) // table descriptor

	if (l2e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L2 entry! Bits[1:0]=")
		uartPutHex64Direct(l2e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// L3 entry (page descriptor)
	l3Base := uintptr(l2e & PTE_ADDR_MASK)
	l3EntryAddr := l3Base + uintptr(l3Idx)*PTE_SIZE
	l3e := *(*uint64)(unsafe.Pointer(l3EntryAddr))
	uartPutsDirect("L3[")
	uartPutHex64Direct(uint64(l3Idx))
	uartPutsDirect("] @ 0x")
	uartPutHex64Direct(uint64(l3EntryAddr))
	uartPutsDirect(" = 0x")
	uartPutHex64Direct(l3e)
	dumpPTEBits(l3e, false) // false = page descriptor

	if (l3e & (PTE_VALID | PTE_TABLE)) != (PTE_VALID | PTE_TABLE) {
		uartPutsDirect("  -> INVALID L3 entry! Bits[1:0]=")
		uartPutHex64Direct(l3e & 3)
		uartPutsDirect("\r\n")
		return
	}

	// Extract and show the physical address
	pa := l3e & PTE_ADDR_MASK
	uartPutsDirect("  -> PA: 0x")
	uartPutHex64Direct(pa)
	uartPutsDirect("\r\n")
}

// dumpPTEBits prints the decoded bit fields of a PTE
// isTable: true for table descriptors (L0-L2), false for page descriptors (L3)
//
//go:nosplit
func dumpPTEBits(pte uint64, isTable bool) {
	uartPutsDirect("\r\n  bits: V=")
	if pte&PTE_VALID != 0 {
		uartPutcDirect('1')
	} else {
		uartPutcDirect('0')
	}

	uartPutsDirect(" T=")
	if pte&PTE_TABLE != 0 {
		uartPutcDirect('1')
	} else {
		uartPutcDirect('0')
	}

	if !isTable {
		// Page descriptor specific bits
		attrIdx := (pte >> 2) & 0x7
		uartPutsDirect(" AttrIdx=")
		uartPutHex64Direct(attrIdx)

		ap := (pte >> 6) & 0x3
		uartPutsDirect(" AP=")
		uartPutHex64Direct(ap)
		switch ap {
		case 0:
			uartPutsDirect("(RW@EL0)")
		case 1:
			uartPutsDirect("(RW@EL1)")
		case 2:
			uartPutsDirect("(RO@EL0)")
		case 3:
			uartPutsDirect("(RO@EL1)")
		}

		sh := (pte >> 8) & 0x3
		uartPutsDirect(" SH=")
		uartPutHex64Direct(sh)
		switch sh {
		case 0:
			uartPutsDirect("(None)")
		case 2:
			uartPutsDirect("(Outer)")
		case 3:
			uartPutsDirect("(Inner)")
		}

		uartPutsDirect(" AF=")
		if pte&PTE_AF != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}

		uartPutsDirect(" PXN=")
		if pte&PTE_PXN != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}

		uartPutsDirect(" UXN=")
		if pte&PTE_UXN != 0 {
			uartPutcDirect('1')
		} else {
			uartPutcDirect('0')
		}
	}

	uartPutsDirect("\r\n")
}

// enableMMU enables the MMU and switches to virtual addressing.
//
// This implementation follows the ARM Trusted Firmware reference exactly:
// https://github.com/ARM-software/arm-trusted-firmware/blob/master/lib/xlat_tables_v2/aarch64/enable_mmu.S
//
// The key sequence is:
//   1. TLB invalidate FIRST (before any register setup)
//   2. Write MAIR_EL1
//   3. Write TCR_EL1
//   4. Write TTBR0_EL1
//   5. DSB ISH + ISB (context synchronization before SCTLR)
//   6. Read/modify/write SCTLR_EL1 to enable MMU
//   7. Single ISB after SCTLR
//
// Previous implementation used UART writes as implicit barriers. This version
// uses explicit barriers per ARM TF reference, with only minimal UART output
// for debugging without relying on Device memory ordering effects.
//
//go:nosplit
func enableMMU() bool {
	uartBase := uintptr(0x09000000)

	if pageTableL0 == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		return false
	}

	// =========================================================================
	// Step 1: TLB invalidate FIRST (ARM TF does this before any register setup)
	// =========================================================================
	asm.InvalidateTlbAll()

	// =========================================================================
	// Step 2: Configure MAIR_EL1
	// =========================================================================
	// MAIR[0] = 0xFF (Normal, Inner/Outer Write-Back Cacheable)
	// MAIR[1] = 0x00 (Device-nGnRnE)
	// MAIR[2] = 0x44 (Normal, Inner/Outer Non-Cacheable)
	mairValue := uint64(0xFF) |      // Attr0: Normal cacheable
		(uint64(0x00) << 8) |  // Attr1: Device
		(uint64(0x44) << 16)   // Attr2: Normal non-cacheable
	asm.WriteMairEl1(mairValue)

	// =========================================================================
	// Step 3: Configure TCR_EL1
	// =========================================================================
	tcrValue := uint64(0)
	tcrValue |= 16 << 0  // T0SZ = 16 (48-bit VA space)
	tcrValue |= 1 << 8   // IRGN0 = 1 (Inner Write-Back Cacheable)
	tcrValue |= 1 << 10  // ORGN0 = 1 (Outer Write-Back Cacheable)
	tcrValue |= 3 << 12  // SH0 = 3 (Inner Shareable)
	tcrValue |= 16 << 16 // T1SZ = 16
	tcrValue |= 1 << 23  // EPD1 = 1 (Disable TTBR1 walks)
	tcrValue |= 2 << 32  // IPS = 2 (40-bit PA space)
	asm.WriteTcrEl1(tcrValue)

	// =========================================================================
	// Step 4: Configure TTBR0_EL1 and TTBR1_EL1
	// =========================================================================
	asm.WriteTtbr1El1(0)
	asm.WriteTtbr0El1(uint64(pageTableL0))

	// =========================================================================
	// Step 5: DSB ISH + ISB (ARM TF uses Inner Shareable barrier)
	// This is the CRITICAL synchronization point before enabling MMU
	// =========================================================================
	asm.DsbIsh()
	asm.Isb()

	// =========================================================================
	// Step 6: Read/modify/write SCTLR_EL1 to enable MMU
	// =========================================================================
	sctlr := asm.ReadSctlrEl1()
	sctlr |= 1 << 0   // M = 1 (MMU enable)
	sctlr &^= 1 << 2  // C = 0 (data cache DISABLED initially)
	sctlr &^= 1 << 12 // I = 0 (instruction cache DISABLED initially)

	asm.WriteSctlrEl1(sctlr)

	// =========================================================================
	// Step 7: Single ISB after SCTLR (per ARM TF reference)
	// =========================================================================
	asm.Isb()

	// MMU is now ON - print a single character to confirm
	*(*uint32)(unsafe.Pointer(uartBase)) = 'M'

	return true
}

// =============================================================================
// Critical Globals Verification
// =============================================================================
// These functions verify that all globals used by the page fault handler are
// properly mapped in the page tables BEFORE any demand paging can occur.
// If any of these are not mapped, the page fault handler would trigger
// a nested page fault (infinite recursion) when trying to access them.

// VerifyCriticalGlobalsMapped walks the page tables to verify all critical
// globals used by HandlePageFault are already mapped.
// Should be called at the start of KernelMain after MMU is enabled.
//
// Returns true if all critical globals are mapped, false if any are unmapped.
// Prints detailed diagnostic information about each global.
func VerifyCriticalGlobalsMapped() bool {
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'W' // W = entered VerifyCriticalGlobalsMapped

	// Simplified verification - just check mappings without verbose output
	// (print() causes page faults if rodata isn't properly mapped)
	allMapped := true

	// 1. Check pageTableL0 pointer itself (stored in BSS)
	pageTableL0Addr := uintptr(unsafe.Pointer(&pageTableL0))
	if getPhysicalAddress(pageTableL0Addr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '1'

	// 2. Check physFrameAllocatorState_global
	physAllocAddr := uintptr(unsafe.Pointer(&physFrameAllocatorState_global))
	if getPhysicalAddress(physAllocAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '2'

	// 3. Check totalKernelPages_global
	totalPagesAddr := uintptr(unsafe.Pointer(&totalKernelPages_global))
	if getPhysicalAddress(totalPagesAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '3'

	// 4. Check inPageFaultHandler_global (re-entrancy guard)
	reentryAddr := uintptr(unsafe.Pointer(&inPageFaultHandler_global))
	if getPhysicalAddress(reentryAddr) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '4'

	// 5. Check mmapSpans array (defined in syscall.go)
	// Skip for now - the verifyMmapSpansMapped function also uses print()
	*(*uint32)(unsafe.Pointer(uartBase)) = '5'

	// 6. Verify the page table L0 base itself is valid and mapped
	if getPhysicalAddress(pageTableL0) == 0 {
		*(*uint32)(unsafe.Pointer(uartBase)) = '!'
		allMapped = false
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '6'

	if allMapped {
		uartPutsDirect("Globals OK\r\n")
	} else {
		uartPutsDirect("GLOBALS FAILED\r\n")
	}

	return allMapped
}

// printHex64ForMMU prints a 64-bit value in hex format (helper for verification)
// Uses print() which is safe after MMU is enabled
// Named differently from kernel.go's printHex64 to avoid redeclaration
func printHex64ForMMU(v uint64) {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[v&0xF]
		v >>= 4
	}
	print(string(buf[:]))
}
