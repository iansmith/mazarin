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

// readTTBR1 reads TTBR1_EL1 (page table base for upper VA range)
// Go: func readTTBR1() uint64
TEXT ·readTTBR1(SB), NOSPLIT, $0-8
	MRS	TTBR1_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// writeTTBR1 writes to TTBR1_EL1
// Go: func writeTTBR1(val uint64)
TEXT ·writeTTBR1(SB), NOSPLIT, $0-8
	MOVD	val+0(FP), R0
	MSR	R0, TTBR1_EL1
	DSB	$15	// DSB SY
	ISB	$15
	RET

// readTCR reads TCR_EL1 (Translation Control Register)
// Go: func readTCR() uint64
TEXT ·readTCR(SB), NOSPLIT, $0-8
	MRS	TCR_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// writeTCR writes to TCR_EL1
// Go: func writeTCR(val uint64)
TEXT ·writeTCR(SB), NOSPLIT, $0-8
	MOVD	val+0(FP), R0
	MSR	R0, TCR_EL1
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

// jumpToKmazarinWithStack sets up SP_EL0 (g0 stack) and SP_EL1 (exception stack),
// installs kmazarin's exception handler (VBAR_EL1), then jumps to the kernel
// entry point in EL1t mode.
//
// Go: func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, vbar uint64)
//
// entry:       kernel entry point (virtual address)
// g0StackPtr:  SP value pointing to argc on g0 stack (VA)
// excStackTop: exception stack top (VA)
// vbar:        kmazarin's ExceptionVectorTable (VA for VBAR_EL1)
TEXT ·jumpToKmazarinWithStack(SB), NOSPLIT, $0-32
	MOVD	entry+0(FP), R0
	MOVD	g0StackPtr+8(FP), R1	// SP_EL0 value
	MOVD	excStackTop+16(FP), R2	// SP_EL1 value
	MOVD	vbar+24(FP), R3		// VBAR_EL1 value
	MOVD	R3, R5			// Save VBAR in R5 (survives SCTLR manipulation)

	// We are currently in EL1h mode (using SP_EL1).
	// Set SP_EL0 (inactive stack) to g0 stack pointer
	MSR	R1, SP_EL0

	// Set SP_EL1 (current/active stack) to exception stack top
	// Use MOV SP, X2 (cannot use MSR for active stack)
	MOVD	R2, RSP

	// Mask all interrupts (DAIF) before jumping to kmazarin.
	// UEFI may have timer interrupts enabled; without masking,
	// an IRQ fires during kmazarin init and hits our WFI-loop
	// IRQ handler, permanently stalling the CPU.
	// MSR DAIFSET, #0xF — mask Debug, SError, IRQ, FIQ
	WORD	$0xD50343DF

	// Clear SCTLR_EL1 alignment check bits that UEFI sets.
	// Go's runtime assumes A=0 (no data alignment check) like Linux.
	// Bit 1 (A) = data alignment check
	// Bit 3 (SA) = SP alignment check for EL1
	MRS	SCTLR_EL1, R3
	MOVD	$0xA, R4	// bits to clear: 1 (A) and 3 (SA)
	BIC	R4, R3, R3	// R3 = R3 & ~R4
	MSR	R3, SCTLR_EL1
	ISB	$15

	// Write MAIR_EL1 to match Cardinal's layout (kmazarin expects this).
	// UEFI uses MAIR[0]=Device, MAIR[3]=Normal, but kmazarin's PTE_ATTR_NORMAL
	// uses index 0 and PTE_ATTR_DEVICE uses index 1 (matching Cardinal).
	// Value: MAIR[0]=0xFF(Normal WB), MAIR[1]=0x00(Device-nGnRnE),
	//        MAIR[2]=0x44(Normal NC), MAIR[3]=0xFF(Normal WB)
	// Diplomat's linear map entries use index 3 → still Normal WB.
	// Diplomat's MMIO entries use index 1 → now Device (was index 0 in UEFI).
	MOVD	$0x00000000FF4400FF, R3
	MSR	R3, MAIR_EL1
	ISB	$15
	// Flush TLB so page walker uses new MAIR attribute indices
	// TLBI VMALLE1 encoding: 0xD508871F
	WORD	$0xD508871F
	DSB	$15
	ISB	$15

	// Switch to EL1t mode (use SP_EL0 for normal execution)
	// MSR SPSel, #0
	WORD	$0xD50040BF

	// Now SP = SP_EL0 = g0 stack pointer (pointing to argc)
	// SP_EL1 = exception stack top

	// Clear TPIDR_EL1 (used by kmazarin overlay as futex call counter)
	MSR	ZR, TPIDR_EL1

	// Install kmazarin's exception handler (VBAR_EL1).
	// This must happen AFTER stacks are set up (SP_EL1 = exception stack)
	// but BEFORE the jump, so kmazarin's handler is active from the first
	// instruction. It handles both data aborts (demand paging for heap)
	// and SVC (clone for thread creation via SyscallClone).
	MSR	R5, VBAR_EL1
	DSB	$15
	ISB	$15

	// Clear registers for clean state
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

	// Jump to kmazarin entry point (_rt0_arm64_linux)
	JMP	(R0)

// readCNTVCT reads the ARM generic timer virtual counter.
// Used for pseudorandom seed generation.
// Go: func readCNTVCT() uint64
TEXT ·readCNTVCT(SB), NOSPLIT, $0-8
	MRS	CNTVCT_EL0, R0
	MOVD	R0, ret+0(FP)
	RET
