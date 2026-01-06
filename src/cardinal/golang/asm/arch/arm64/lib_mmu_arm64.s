// lib_mmu.s - MMU system register access functions in Go/Plan9 assembly
//
// This file contains functions for MMU configuration and TLB/cache maintenance.
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

// read_ttbr0_el1() uint64
// Read TTBR0_EL1 (Translation Table Base Register 0)
// For ABI0 functions called from internal ABI, return value must be on stack
TEXT read_ttbr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	TTBR0_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// read_ttbr1_el1() uint64
// Read TTBR1_EL1 (Translation Table Base Register 1)
// For ABI0 functions called from internal ABI, return value must be on stack
TEXT read_ttbr1_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	TTBR1_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// write_ttbr0_el1(value uint64)
// Write TTBR0_EL1
// Must be 4KB aligned, lower 12 bits ignored by hardware
// Argument arrives in R0 (Go register ABI)
TEXT write_ttbr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, TTBR0_EL1
	ISB	$15
	RET

// write_ttbr1_el1(value uint64)
// Write TTBR1_EL1
// Even when EPD1=1 (TTBR1 disabled), should be initialized to a safe value
// Argument arrives in R0 (Go register ABI)
TEXT write_ttbr1_el1(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, TTBR1_EL1
	ISB	$15
	RET

// ============================================================================
// MAIR_EL1 - Memory Attribute Indirection Register
// ============================================================================

// read_mair_el1() uint64
// Read MAIR_EL1
TEXT read_mair_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	MAIR_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// write_mair_el1(value uint64)
// Write MAIR_EL1
// MAIR_EL1 format: 8 attributes, 8 bits each
//   Attr0 (bits 7:0)   = Normal, Inner/Outer Write-Back Cacheable (0xFF)
//   Attr1 (bits 15:8)  = Device-nGnRnE (0x00)
// Argument arrives in R0 (Go register ABI)
TEXT write_mair_el1(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, MAIR_EL1
	ISB	$15
	RET

// ============================================================================
// TCR_EL1 - Translation Control Register
// ============================================================================

// read_tcr_el1() uint64
// Read TCR_EL1
TEXT read_tcr_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	TCR_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// write_tcr_el1(value uint64)
// Write TCR_EL1
// Argument arrives in R0 (Go register ABI)
TEXT write_tcr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, TCR_EL1
	ISB	$15
	RET

// ============================================================================
// TLB Invalidation
// ============================================================================

// invalidate_tlb_all()
// Invalidate entire TLB
// Uses TLBI VMALLE1 (not ALLE1) because this can be executed with MMU disabled
TEXT invalidate_tlb_all(SB), NOSPLIT|NOFRAME, $0-0
	DSB	$15		// DSB SY - ensure all memory accesses complete
	// TLBI VMALLE1 - encoded as: 0xD508871F
	WORD	$0xD508871F
	DSB	$15		// Ensure TLB invalidation completes
	ISB	$15
	RET

// invalidate_tlb_va(addr uintptr)
// Invalidate TLB entry for specific virtual address
// This is much faster than invalidating entire TLB when mapping a single page
// Argument arrives in R0 (Go register ABI)
TEXT invalidate_tlb_va(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	LSR	$12, R0, R0	// Convert to page number (VA >> 12)
	DSB	$10		// DSB ISHST - ensure prior writes complete
	// TLBI VAE1, x0 - encoded as: sys #0, c8, c7, #1, x0
	// Opcode: 0xD5088721 | (Rt)
	WORD	$0xD5088720	// TLBI VAE1, X0
	DSB	$11		// DSB ISH - ensure TLB invalidation completes
	ISB	$15
	RET

// ============================================================================
// Data Cache Maintenance
// ============================================================================

// clean_dcache_va(addr uintptr)
// Clean data cache for specific virtual address
// Ensures modified page table entries are visible to hardware page table walker
// Argument arrives in R0 (Go register ABI)
TEXT clean_dcache_va(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	// DC CVAC, X0 - encoded as: sys #3, c7, c10, #1, x0
	WORD	$0xD50B7A20	// DC CVAC, X0
	DSB	$11		// DSB ISH - ensure cache clean completes
	RET

// CleanDataCacheVA(addr uintptr)
// Clean data cache by virtual address (duplicate for Go naming convention)
// Argument arrives in R0 (Go register ABI)
TEXT CleanDataCacheVA(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	WORD	$0xD50B7A20	// DC CVAC, X0
	DSB	$15		// DSB SY
	RET

// DcCvau(addr uintptr)
// DC CVAU - Data Cache Clean by VA to Point of Unification
// Argument arrives in R0 (Go register ABI)
TEXT DcCvau(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	// DC CVAU, X0 - encoded as: sys #3, c7, c11, #1, x0
	WORD	$0xD50B7B20	// DC CVAU, X0
	RET

// ============================================================================
// Instruction Cache Maintenance
// ============================================================================

// InvalidateInstructionCacheAll()
// Invalidate all instruction caches
// Used after relocating exception vectors or self-modifying code
TEXT InvalidateInstructionCacheAll(SB), NOSPLIT|NOFRAME, $0-0
	WORD	$0xD508751F	// IC IALLU
	DSB	$15		// DSB SY
	ISB	$15
	RET

// IcIvau(addr uintptr)
// IC IVAU - Instruction Cache Invalidate by VA to Point of Unification
// Argument arrives in R0 (Go register ABI)
TEXT IcIvau(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	// IC IVAU, X0 - encoded as: sys #3, c7, c5, #1, x0
	WORD	$0xD50B7520	// IC IVAU, X0
	RET
