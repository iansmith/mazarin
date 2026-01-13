// lib_mmu.s - MMU system register access functions in Go/Plan9 assembly
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains atomic MMU primitives that cannot be decomposed further.
// Each function is a single-purpose operation accessing ARM64 system registers.
//
// Functions are organized by category:
//   - TTBR: Translation table base register access
//   - MAIR: Memory attribute configuration
//   - TCR: Translation control register
//   - TLB: Translation lookaside buffer invalidation
//   - Data Cache: Cache maintenance operations
//   - Instruction Cache: Instruction cache maintenance
//
// ABI NOTES:
// - These are abi0 functions. Go generates wrappers to call them from internal ABI.
// - Arguments: The wrapper passes args in registers AND on stack. Use either.
// - Return values: MUST be stored to ret+0(FP). The wrapper reads from stack, not R0.
// - Write-only functions: No return value needed, can use R0 directly from wrapper.
//
// Migrated from asm/aarch64/lib.s

#include "textflag.h"

// ============================================================================
// TTBR - Translation Table Base Registers
// ============================================================================

// ============================================================================
// read_ttbr0_el1() - Read Translation Table Base Register 0
// ============================================================================
// Read TTBR0_EL1 (Translation Table Base Register 0)
// For ABI0 functions called from internal ABI, return value must be on stack
//
// Segments:
//   1. Read TTBR0_EL1 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_ttbr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read TTBR0_EL1
	MRS	TTBR0_EL1, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// read_ttbr1_el1() - Read Translation Table Base Register 1
// ============================================================================
// Read TTBR1_EL1 (Translation Table Base Register 1)
// For ABI0 functions called from internal ABI, return value must be on stack
//
// Segments:
//   1. Read TTBR1_EL1 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_ttbr1_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read TTBR1_EL1
	MRS	TTBR1_EL1, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_ttbr0_el1(value uint64) - Write Translation Table Base Register 0
// ============================================================================
// Write TTBR0_EL1
// Must be 4KB aligned, lower 12 bits ignored by hardware
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to TTBR0_EL1
//   2. Issue instruction barrier to ensure visibility
//   3. Return to caller
//
TEXT write_ttbr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to TTBR0_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, TTBR0_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// write_ttbr1_el1(value uint64) - Write Translation Table Base Register 1
// ============================================================================
// Write TTBR1_EL1
// Even when EPD1=1 (TTBR1 disabled), should be initialized to a safe value
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to TTBR1_EL1
//   2. Issue instruction barrier to ensure visibility
//   3. Return to caller
//
TEXT write_ttbr1_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to TTBR1_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, TTBR1_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// MAIR_EL1 - Memory Attribute Indirection Register
// ============================================================================

// ============================================================================
// read_mair_el1() - Read Memory Attribute Indirection Register
// ============================================================================
// Read MAIR_EL1
// For ABI0 functions called from internal ABI, return value must be on stack
//
// Segments:
//   1. Read MAIR_EL1 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_mair_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read MAIR_EL1
	MRS	MAIR_EL1, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_mair_el1(value uint64) - Write Memory Attribute Indirection Register
// ============================================================================
// Write MAIR_EL1
// MAIR_EL1 format: 8 attributes, 8 bits each
//   Attr0 (bits 7:0)   = Normal, Inner/Outer Write-Back Cacheable (0xFF)
//   Attr1 (bits 15:8)  = Device-nGnRnE (0x00)
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to MAIR_EL1
//   2. Issue instruction barrier to ensure visibility
//   3. Return to caller
//
TEXT write_mair_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to MAIR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, MAIR_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// TCR_EL1 - Translation Control Register
// ============================================================================

// ============================================================================
// read_tcr_el1() - Read Translation Control Register
// ============================================================================
// Read TCR_EL1
// For ABI0 functions called from internal ABI, return value must be on stack
//
// Segments:
//   1. Read TCR_EL1 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_tcr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read TCR_EL1
	MRS	TCR_EL1, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_tcr_el1(value uint64) - Write Translation Control Register
// ============================================================================
// Write TCR_EL1
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to TCR_EL1
//   2. Issue instruction barrier to ensure visibility
//   3. Return to caller
//
TEXT write_tcr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to TCR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, TCR_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// TLB Invalidation
// ============================================================================

// ============================================================================
// invalidate_tlb_all() - Invalidate Entire TLB
// ============================================================================
// Invalidate entire TLB
// Uses TLBI VMALLE1 (not ALLE1) because this can be executed with MMU disabled
//
// Segments:
//   1. Issue data barrier to ensure prior memory operations complete
//   2. Invalidate entire TLB (TLBI VMALLE1)
//   3. Issue data barrier to ensure TLB invalidation completes
//   4. Issue instruction barrier
//   5. Return to caller
//
TEXT invalidate_tlb_all(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Pre-invalidation barrier
	DSB	$15		// DSB SY - ensure all memory accesses complete

	// Segment 2: Invalidate TLB
	// TLBI VMALLE1 - encoded as: 0xD508871F
	WORD	$0xD508871F

	// Segment 3: Post-invalidation data barrier
	DSB	$15		// Ensure TLB invalidation completes

	// Segment 4: Instruction barrier
	ISB	$15

	// Segment 5: Return
	RET

// ============================================================================
// invalidate_tlb_va(addr uintptr) - Invalidate TLB Entry for Virtual Address
// ============================================================================
// Invalidate TLB entry for specific virtual address
// This is much faster than invalidating entire TLB when mapping a single page
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Convert virtual address to page number
//   2. Issue store barrier to ensure prior writes complete
//   3. Invalidate TLB entry (TLBI VAE1)
//   4. Issue data barrier to ensure TLB invalidation completes
//   5. Issue instruction barrier
//   6. Return to caller
//
TEXT invalidate_tlb_va(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Convert to page number
	// R0 already contains addr from Go register ABI
	LSR	$12, R0, R0	// Convert to page number (VA >> 12)

	// Segment 2: Pre-invalidation barrier
	DSB	$10		// DSB ISHST - ensure prior writes complete

	// Segment 3: Invalidate TLB entry
	// TLBI VAE1, x0 - encoded as: sys #0, c8, c7, #1, x0
	// Opcode: 0xD5088721 | (Rt)
	WORD	$0xD5088720	// TLBI VAE1, X0

	// Segment 4: Post-invalidation data barrier
	DSB	$11		// DSB ISH - ensure TLB invalidation completes

	// Segment 5: Instruction barrier
	ISB	$15

	// Segment 6: Return
	RET

// ============================================================================
// Data Cache Maintenance
// ============================================================================

// ============================================================================
// clean_dcache_va(addr uintptr) - Clean Data Cache by Virtual Address
// ============================================================================
// Clean data cache for specific virtual address
// Ensures modified page table entries are visible to hardware page table walker
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Issue data cache clean operation (DC CVAC)
//   2. Issue data barrier to ensure cache clean completes
//   3. Return to caller
//
TEXT clean_dcache_va(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Clean data cache
	// R0 already contains addr from Go register ABI
	// DC CVAC, X0 - encoded as: sys #3, c7, c10, #1, x0
	WORD	$0xD50B7A20	// DC CVAC, X0

	// Segment 2: Barrier
	DSB	$11		// DSB ISH - ensure cache clean completes

	// Segment 3: Return
	RET

// ============================================================================
// CleanDataCacheVA(addr uintptr) - Clean Data Cache by VA (Go naming convention)
// ============================================================================
// Clean data cache by virtual address (duplicate for Go naming convention)
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Issue data cache clean operation (DC CVAC)
//   2. Issue system-wide data barrier
//   3. Return to caller
//
TEXT CleanDataCacheVA(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Clean data cache
	// R0 already contains addr from Go register ABI
	WORD	$0xD50B7A20	// DC CVAC, X0

	// Segment 2: System-wide barrier
	DSB	$15		// DSB SY

	// Segment 3: Return
	RET

// ============================================================================
// DcCvau(addr uintptr) - Data Cache Clean to Point of Unification
// ============================================================================
// DC CVAU - Data Cache Clean by VA to Point of Unification
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Issue DC CVAU instruction
//   2. Return to caller
//
TEXT DcCvau(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Clean data cache to PoU
	// R0 already contains addr from Go register ABI
	// DC CVAU, X0 - encoded as: sys #3, c7, c11, #1, x0
	WORD	$0xD50B7B20	// DC CVAU, X0

	// Segment 2: Return
	RET

// ============================================================================
// Instruction Cache Maintenance
// ============================================================================

// ============================================================================
// InvalidateInstructionCacheAll() - Invalidate All Instruction Caches
// ============================================================================
// Invalidate all instruction caches
// Used after relocating exception vectors or self-modifying code
//
// Segments:
//   1. Issue instruction cache invalidate (IC IALLU)
//   2. Issue data barrier to ensure invalidation completes
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT InvalidateInstructionCacheAll(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Invalidate instruction cache
	WORD	$0xD508751F	// IC IALLU

	// Segment 2: Data barrier
	DSB	$15		// DSB SY

	// Segment 3: Instruction barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// IcIvau(addr uintptr) - Instruction Cache Invalidate to Point of Unification
// ============================================================================
// IC IVAU - Instruction Cache Invalidate by VA to Point of Unification
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Issue IC IVAU instruction
//   2. Return to caller
//
TEXT IcIvau(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Invalidate instruction cache to PoU
	// R0 already contains addr from Go register ABI
	// IC IVAU, X0 - encoded as: sys #3, c7, c5, #1, x0
	WORD	$0xD50B7520	// IC IVAU, X0

	// Segment 2: Return
	RET
