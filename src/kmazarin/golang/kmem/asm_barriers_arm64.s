//go:build qemuvirt && aarch64

// asm_barriers_arm64.s - ARM64 memory barriers for page table manipulation
//
// These functions implement the barriers and TLB invalidation needed
// when modifying page tables.

#include "textflag.h"

// dsbSYAsm - Data Synchronization Barrier (full system)
// Ensures all memory accesses complete before continuing.
TEXT ·dsbSYAsm(SB), NOSPLIT, $0
	// DSB SY = 0xD5033F9F
	WORD	$0xD5033F9F
	RET

// tlbiVAE1ISAsm - Invalidate ALL TLB entries for current VMID at EL1
// Using TLBI VMALLE1 (not ALLE1!) because ALLE1 requires EL2+
// va parameter is ignored but kept for API compatibility
TEXT ·tlbiVAE1ISAsm(SB), NOSPLIT, $0-8
	// TLBI VMALLE1 - encoded as: 0xD508871F (same as Cardinal uses)
	WORD	$0xD508871F
	RET

// isbSYAsm - Instruction Synchronization Barrier
// Ensures all instruction cache and TLB maintenance operations
// complete before fetching more instructions.
TEXT ·isbSYAsm(SB), NOSPLIT, $0
	ISB	$15
	RET

// readTTBR1EL1 - Read TTBR1_EL1 register
// Returns the physical address of the TTBR1 L0 table
TEXT ·readTTBR1EL1(SB), NOSPLIT, $0-8
	MRS	TTBR1_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// dcCIVACAsm - Clean and Invalidate Data Cache by VA to PoC
// Ensures page table entry is written to memory (visible to hardware walker)
TEXT ·dcCIVACAsm(SB), NOSPLIT, $0-8
	MOVD	va+0(FP), R0
	// DC CIVAC, X0 = 0xD50B7E20
	WORD	$0xD50B7E20
	RET
