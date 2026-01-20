package main

import (
	"unsafe"

	"cardinal/asm"
	"cardinal/constants"
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

// Page table structure (TTBR0 - user/low memory)
var (
	pageTableL0 uintptr   // Level 0 table (PGD)
	pageTableL1 uintptr   // Level 1 table (PUD)
	pageTableL2 []uintptr // Level 2 tables (PMD) - allocated as needed
	pageTableL3 []uintptr // Level 3 tables (PT) - allocated as needed
)

// Kernel page table structure (TTBR1 - kernel/high memory)
var (
	kernelPageTableL0 uintptr   // Kernel L0 table (for TTBR1)
	kernelPageTableL1 uintptr   // Kernel L1 table (index 511)
	kernelPageTableL2 []uintptr // Kernel L2 tables - allocated as needed
	kernelPageTableL3 []uintptr // Kernel L3 tables - allocated as needed
)

// Kmazarin size tracking
// Enforces the 64MB total limit (binary + heap combined)
var (
	kmazarinAllocatedBytes uintptr // Total bytes allocated for kmazarin (includes binary + heap)
)

// Dynamically computed memory layout values
// These are derived from LinkerKmazarinSize at runtime to avoid hardcoded offsets
// that can become stale when kmazarin binary size changes.
var (
	// computedTTBR1RegionVA is where Cardinal's TTBR1 page tables are mapped
	// for kmazarin to access. Computed as page-aligned end of kmazarin static region.
	computedTTBR1RegionVA uintptr

	// computedPTPoolStart/End define the page table pool kmazarin uses
	// for demand paging. Placed immediately after the TTBR1 region.
	computedPTPoolStart uintptr
	computedPTPoolEnd   uintptr
)

// initComputedMemoryLayout derives memory layout values from LinkerKmazarinSize.
// This MUST be called before setupKernelDemandPaging() uses these values.
// The layout is:
//   KernelTextBase + LinkerKmazarinSize = end of kmazarin static region
//   Page-align up = computedTTBR1RegionVA (64KB for TTBR1 tables)
//   + TTBR1 region size = computedPTPoolStart (512KB for PT allocation)
//
//go:nosplit
func initComputedMemoryLayout() {
	// KernelTextBase is computed from constants at compile time
	kernelTextBase := uintptr(constants.KernelTextBase)

	// LinkerKmazarinSize is patched at build time from kmazarin.elf
	// It represents the total memory footprint of kmazarin's static regions
	kmazarinEndVA := kernelTextBase + uintptr(LinkerKmazarinSize)

	// Page-align up to get TTBR1 region start
	computedTTBR1RegionVA = (kmazarinEndVA + PAGE_SIZE - 1) &^ (PAGE_SIZE - 1)

	// PT pool starts after TTBR1 region (64KB for up to 16 page tables)
	const ttbr1RegionSize = 0x10000 // 64KB
	computedPTPoolStart = computedTTBR1RegionVA + ttbr1RegionSize

	// PT pool is 512KB (128 pages)
	const ptPoolSize = 0x80000 // 512KB
	computedPTPoolEnd = computedPTPoolStart + ptPoolSize
}

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

