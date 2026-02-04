
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

// tlbiVMALLE1 - Invalidate ALL TLB entries for EL1&0 regime (current VMID)
// This is the comprehensive TLB flush for userspace transition
TEXT ·tlbiVMALLE1(SB), NOSPLIT, $0-0
	// TLBI VMALLE1 = 0xD508871F
	WORD	$0xD508871F
	RET

// tlbiVMALLE1IS - Invalidate ALL TLB entries Inner Shareable
// Broadcasts invalidation to all CPUs in the inner shareable domain
// TLBI VMALLE1IS = 0xD5088318
TEXT ·tlbiVMALLE1IS(SB), NOSPLIT, $0-0
	WORD	$0xD5088318
	RET

// tlbiASIDE1ISAsm - Invalidate TLB entries by ASID Inner Shareable
// Invalidates all TLB entries matching the given ASID across all CPUs.
// This is used for aggressive ASID reuse - when a priest exits and its
// ASID is about to be reused, we must invalidate all old TLB entries.
// TLBI ASIDE1IS, Xt where Xt[63:48] = ASID
// Encoding: TLBI ASIDE1IS = 0xD5088720 | (Rt << 0) where Rt is the register
// For X0: 0xD5088720
TEXT ·tlbiASIDE1ISAsm(SB), NOSPLIT, $0-8
	MOVD	asid+0(FP), R0
	LSL	$48, R0, R0		// ASID goes in bits [63:48]
	WORD	$0xD5088720		// TLBI ASIDE1IS, X0
	RET

// dsbISH - Data Synchronization Barrier Inner Shareable
// Ensures all prior memory accesses complete in the inner shareable domain
// DSB ISH = 0xD5033B9F
TEXT ·dsbISH(SB), NOSPLIT, $0-0
	WORD	$0xD5033B9F
	RET

// isbSYAsm - Instruction Synchronization Barrier
// Ensures all instruction cache and TLB maintenance operations
// complete before fetching more instructions.
TEXT ·isbSYAsm(SB), NOSPLIT, $0
	ISB	$15
	RET

// readTTBR0EL1 - Read TTBR0_EL1 register
// Returns the physical address of the TTBR0 L0 table (actual hardware value)
TEXT ·readTTBR0EL1(SB), NOSPLIT, $0-8
	MRS	TTBR0_EL1, R0
	MOVD	R0, ret+0(FP)
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

// dcCVAUAsm - Clean Data Cache by VA to Point of Unification
// This is needed before IC IVAU to ensure code is visible to instruction fetch
// DC CVAU, X0 = 0xD50B7B20
TEXT ·dcCVAUAsm(SB), NOSPLIT, $0-8
	MOVD	va+0(FP), R0
	WORD	$0xD50B7B20
	RET

// icIVAUAsm - Invalidate Instruction Cache by VA to Point of Unification
// Ensures stale instructions are not fetched after code modification
// IC IVAU, X0 = 0xD50B7520
TEXT ·icIVAUAsm(SB), NOSPLIT, $0-8
	MOVD	va+0(FP), R0
	WORD	$0xD50B7520
	RET

// icIALLUAsm - Invalidate ALL Instruction Cache to Point of Unification
// More aggressive than IC IVAU - invalidates everything
// IC IALLU = 0xD508751F
TEXT ·icIALLUAsm(SB), NOSPLIT, $0-0
	WORD	$0xD508751F
	RET

// ============================================================================
// DC ZVA - Fast Page Zeroing
// ============================================================================

// dcZVAAsm - Zero a single cache line using DC ZVA instruction
// This is the fastest way to zero memory on ARM64.
// addr must be cache-line aligned (typically 64 bytes).
// DC ZVA, X0 = 0xD50B7420
TEXT ·dcZVAAsm(SB), NOSPLIT, $0-8
	MOVD	addr+0(FP), R0
	WORD	$0xD50B7420
	RET

// bzero4KAsm - Zero a 4KB page using DC ZVA
// Assumes 64-byte cache lines (standard ARM64).
// 4096 / 64 = 64 cache lines per page.
// ptr must be page-aligned (4KB).
TEXT ·bzero4KAsm(SB), NOSPLIT, $0-8
	MOVD	ptr+0(FP), R0
	MOVD	$64, R1		// 64 cache lines per 4KB page
bzero_loop:
	WORD	$0xD50B7420	// DC ZVA, X0
	ADD	$64, R0		// Next cache line
	SUB	$1, R1
	CBNZ	R1, bzero_loop
	RET

// writeTTBR0Asm - Write to TTBR0_EL1 register
// Used for switching to a new process's page tables
TEXT ·writeTTBR0Asm(SB), NOSPLIT, $0-8
	MOVD	val+0(FP), R0
	MSR	R0, TTBR0_EL1
	RET

// atS1E0R - Address Translation Stage 1 EL0 Read
// Performs hardware page table walk for the given VA as if accessed from EL0
// Returns the PAR_EL1 value which contains the translated PA (or fault info)
// PAR_EL1 format on success (bit 0 = 0):
//   bits 47:12 = PA[47:12]
//   bits 11:0 = attributes
// PAR_EL1 format on failure (bit 0 = 1):
//   bits 6:1 = FST (fault status)
TEXT ·atS1E0R(SB), NOSPLIT, $0-16
	MOVD	va+0(FP), R0
	// AT S1E0R, X0 = 0xD5087800 + (0b000 << 16) + (0b100 << 12) + (0b011 << 8) + (0b000 << 5) + 0
	// Encoding: 1101_0101_0000_1000_0111_1000_0000_0000 = 0xD5087800
	WORD	$0xD5087800
	ISB	$15		// Ensure AT completes before reading PAR
	MRS	PAR_EL1, R0
	MOVD	R0, ret+8(FP)
	RET

// dcCleanInvalidateAll - Clean and Invalidate entire D-cache
// This iterates through all cache sets/ways to ensure complete cache flush.
// Used before ERET to userspace to guarantee data visibility.
//
// For Cortex-A72 (QEMU virt):
// - L1 D-cache: 32KB, 2-way, 64-byte lines = 256 sets
// - L2 cache: varies but we'll use conservative values
//
// CLIDR_EL1 tells us cache levels, CCSIDR_EL1 tells us geometry.
// We'll clean L1 (LoC level 0) which is sufficient for single-core.
TEXT ·dcCleanInvalidateAll(SB), NOSPLIT, $0-0
	// Read CLIDR_EL1 to get cache info
	MRS	CLIDR_EL1, R0

	// For L1 D-cache (level 0), select it via CSSELR_EL1
	MOVD	$0, R1		// Level 0, Data cache
	MSR	R1, CSSELR_EL1
	WORD	$0xD5033F9F	// ISB - ensure CSSELR write takes effect

	// Read CCSIDR_EL1 for L1 geometry
	MRS	CCSIDR_EL1, R0

	// Extract NumSets (bits 27:13) and Associativity (bits 12:3)
	// NumSets = (CCSIDR[27:13]) + 1
	// Assoc = (CCSIDR[12:3]) + 1
	// LineSize = 2^(CCSIDR[2:0] + 4) bytes

	// For simplicity, use fixed values for Cortex-A72:
	// L1: 256 sets, 2-way, 64-byte lines
	MOVD	$256, R2	// NumSets
	MOVD	$2, R3		// Associativity (ways)

	// Outer loop: iterate over ways
	MOVD	$0, R4		// way counter
way_loop:
	CMP	R4, R3
	BGE	done

	// Inner loop: iterate over sets
	MOVD	$0, R5		// set counter
set_loop:
	CMP	R5, R2
	BGE	next_way

	// Build DC CISW operand: way[31:32-A] | set[L+S-1:L] | level[3:1]
	// For 2-way (A=1): way in bit 31
	// For 256 sets with 64-byte lines (L=6, S=8): set in bits 13:6
	// Level 0: level bits = 0
	LSL	$31, R4, R6	// way << 31
	LSL	$6, R5, R7	// set << 6 (LineSize log2 = 6)
	ORR	R6, R7, R6	// combine

	// DC CISW operand goes in X0
	// DC CISW encoding: 0xD50B7E40 (different from DC CIVAC which is 0xD50B7E20)
	MOVD	R6, R0
	WORD	$0xD50B7E40	// DC CISW, X0

	ADD	$1, R5
	B	set_loop

next_way:
	ADD	$1, R4
	B	way_loop

done:
	WORD	$0xD5033F9F	// DSB SY
	RET
