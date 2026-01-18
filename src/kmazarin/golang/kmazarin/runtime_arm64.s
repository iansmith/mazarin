
// runtime_arm64.s - Runtime support functions for kmazarin

#include "textflag.h"

// ============================================================================
// getAuxval - Get auxiliary vector value by tag
// ============================================================================
// func getAuxval(tag uint64) uint64
//
// DEPRECATED: This function is not used. All boot information is now passed
// via RuntimeConfig structure in StartupParams, which provides computed values
// instead of hardcoded addresses. RuntimeConfig includes DtbVirtAddr and other
// fields that replace custom auxv entries.
//
// Keeping this stub for now in case it's needed in the future, but it returns
// 0 for all tags since RuntimeConfig is the proper way to access boot info.

TEXT ·getAuxval(SB), NOSPLIT, $0-16
	MOVD	tag+0(FP), R0		// Load tag parameter

	// All tags return 0 - use RuntimeConfig instead
	MOVD	$0, R0
	MOVD	R0, ret+8(FP)
	RET

// ============================================================================
// readTTBR0 - Read TTBR0_EL1 register
// ============================================================================
// func readTTBR0() uint64
//
// Returns the physical address of the TTBR0 (Cardinal) L0 page table.

TEXT ·readTTBR0(SB), NOSPLIT, $0-8
	MRS	TTBR0_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// dsb - Data Synchronization Barrier
// ============================================================================
// func dsb()
//
// Ensures all memory accesses complete before proceeding.

TEXT ·dsb(SB), NOSPLIT, $0-0
	DSB	$15		// DSB SY (full system barrier)
	RET

// ============================================================================
// isb - Instruction Synchronization Barrier
// ============================================================================
// func isb()
//
// Flushes pipeline and ensures all subsequent instructions fetch fresh.

TEXT ·isb(SB), NOSPLIT, $0-0
	ISB	$15		// ISB SY
	RET

// ============================================================================
// invalidateTLB - Invalidate entire TLB
// ============================================================================
// func invalidateTLB()
//
// Invalidates all TLB entries for all ASIDs at all ELs.

TEXT ·invalidateTLB(SB), NOSPLIT, $0-0
	DSB	$11			// DSB ISH - ensure prior stores complete
	WORD	$0xD508871F		// TLBI VMALLE1 - invalidate all TLB entries
	DSB	$11			// DSB ISH - ensure TLB invalidation completes
	ISB	$15			// ISB SY - synchronize context
	RET
