//go:build arm64

// diplomat/main/pagetable_arm64.s
// ARM64 page table and control register operations
//
// ARM64 uses TTBR0_EL1 (Translation Table Base Register 0) instead of x86's CR3.
// TLB invalidation uses TLBI instructions instead of reloading CR3.

#include "textflag.h"

// readTTBR0 reads TTBR0_EL1 (page table base for lower VA range)
// Go: func readTTBR0() uint64
TEXT ·readTTBR0(SB), NOSPLIT, $0-8
	MRS	TTBR0_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// writeTTBR0 writes to TTBR0_EL1
// Go: func writeTTBR0(val uint64)
TEXT ·writeTTBR0(SB), NOSPLIT, $0-8
	MOVD	val+0(FP), R0
	MSR	R0, TTBR0_EL1
	// Data synchronization barrier to ensure write completes
	DSB	$15	// DSB SY (full system barrier)
	// Instruction synchronization barrier
	ISB	$15
	RET

// tlbiVmalle1 invalidates all TLB entries for EL1
// Go: func tlbiVmalle1()
TEXT ·tlbiVmalle1(SB), NOSPLIT, $0-0
	// TLBI VMALLE1 - Invalidate all TLB entries for EL0/EL1
	// The assembler doesn't have a nice mnemonic, so we use WORD
	// TLBI VMALLE1 encoding: 1101 0101 0000 1000 1000 0111 0001 1111
	//                        = 0xD508871F
	WORD	$0xD508871F
	RET

// dsb executes a data synchronization barrier (DSB SY)
// Go: func dsb()
TEXT ·dsb(SB), NOSPLIT, $0-0
	DSB	$15	// DSB SY (full system barrier)
	RET

// isb executes an instruction synchronization barrier
// Go: func isb()
TEXT ·isb(SB), NOSPLIT, $0-0
	ISB	$15
	RET

// jumpToEntry jumps to a kernel entry point. Does not return.
// Go: func jumpToEntry(entry uint64)
TEXT ·jumpToEntry(SB), NOSPLIT, $0-8
	MOVD	entry+0(FP), R0

	// Clear registers for clean state
	// Note: R18 is platform register (reserved), don't modify
	// R28 = g (Go's goroutine pointer), R29 = FP, R30 = LR
	MOVD	ZR, R1
	MOVD	ZR, R2
	MOVD	ZR, R3
	MOVD	ZR, R4
	MOVD	ZR, R5
	MOVD	ZR, R6
	MOVD	ZR, R7
	MOVD	ZR, R8
	MOVD	ZR, R9
	MOVD	ZR, R10
	MOVD	ZR, R11
	MOVD	ZR, R12
	MOVD	ZR, R13
	MOVD	ZR, R14
	MOVD	ZR, R15
	MOVD	ZR, R16
	MOVD	ZR, R17
	// Skip R18 (platform register)
	MOVD	ZR, R19
	MOVD	ZR, R20
	MOVD	ZR, R21
	MOVD	ZR, R22
	MOVD	ZR, R23
	MOVD	ZR, R24
	MOVD	ZR, R25
	MOVD	ZR, R26
	MOVD	ZR, R27

	// Jump to entry point (no return)
	JMP	(R0)

// jumpToKernelWithTTBR0 switches to new page tables and jumps to kernel entry.
// Identity mapping for low memory must be present in the new page tables
// so this code can continue executing after the TTBR0 switch.
// Does not return.
// Go: func jumpToKernelWithTTBR0(l0Phys, virtEntry uint64)
TEXT ·jumpToKernelWithTTBR0(SB), NOSPLIT, $0-16
	MOVD	l0Phys+0(FP), R0
	MOVD	virtEntry+8(FP), R1

	// Write new page table base to TTBR0_EL1
	// This immediately switches the virtual-to-physical mapping.
	// Because we include identity mapping for low memory in the new tables,
	// this code (running at low physical addresses) continues to work.
	MSR	R0, TTBR0_EL1

	// Data synchronization barrier to ensure TTBR0 write completes
	DSB	$15	// DSB SY

	// Invalidate all TLB entries
	// TLBI VMALLE1 encoding: 0xD508871F
	WORD	$0xD508871F

	// Data synchronization barrier to ensure TLB invalidation completes
	DSB	$15	// DSB SY

	// Instruction synchronization barrier to flush pipeline
	ISB	$15

	// Clear registers for clean state (except R1 which has entry)
	// Note: R18 is platform register (reserved), don't modify
	MOVD	ZR, R0
	MOVD	ZR, R2
	MOVD	ZR, R3
	MOVD	ZR, R4
	MOVD	ZR, R5
	MOVD	ZR, R6
	MOVD	ZR, R7
	MOVD	ZR, R8
	MOVD	ZR, R9
	MOVD	ZR, R10
	MOVD	ZR, R11
	MOVD	ZR, R12
	MOVD	ZR, R13
	MOVD	ZR, R14
	MOVD	ZR, R15
	MOVD	ZR, R16
	MOVD	ZR, R17
	// Skip R18 (platform register)
	MOVD	ZR, R19
	MOVD	ZR, R20
	MOVD	ZR, R21
	MOVD	ZR, R22
	MOVD	ZR, R23
	MOVD	ZR, R24
	MOVD	ZR, R25
	MOVD	ZR, R26
	MOVD	ZR, R27

	// Jump to kernel virtual entry point (no return)
	JMP	(R1)
