//go:build arm64

// smp_entry_arm64.s - Secondary CPU entry point for SMP boot
//
// This is the entry point for secondary CPUs when woken by PSCI CPU_ON.
// Each secondary CPU:
// 1. Reads its CPU ID from MPIDR_EL1
// 2. Sets up stacks (SP_EL1 and SP_EL0) from per-CPU data
// 3. Installs exception vectors
// 4. Enables FPU
// 5. Disables alignment checking
// 6. Initializes GIC CPU Interface (GICC) - CRITICAL for proper IRQ delivery
// 7. Marks itself as online
// 8. Enters idle loop waiting for work

#include "textflag.h"

// ============================================================================
// Secondary CPU Entry Point
// ============================================================================
// Called by PSCI CPU_ON with:
//   X0 = context_id (passed from CPU_ON call, we use CPU ID)
//
// func secondaryCPUEntry()
TEXT ·secondaryCPUEntry(SB), NOSPLIT|NOFRAME, $0
	// ========================================
	// Step 1: Get CPU ID from MPIDR_EL1
	// ========================================
	// Read MPIDR_EL1 and extract Aff0 (bits 7:0)
	MRS	MPIDR_EL1, R0
	AND	$0xFF, R0, R19		// R19 = CPU ID (preserved across calls)

	// ========================================
	// Step 2: Compute per-CPU data address
	// ========================================
	// Address = &perCPUData[0] + (cpuID * PerCPUSize)
	MOVD	$·perCPUData(SB), R20	// R20 = base of perCPUData array
	MOVD	·PerCPUSize(SB), R1	// R1 = size of one PerCPU struct
	MUL	R1, R19, R1		// R1 = cpuID * PerCPUSize
	ADD	R1, R20, R20		// R20 = pointer to our PerCPU struct

	// ========================================
	// Step 3: Set up stacks
	// ========================================
	// Load exception stack top (SP_EL1) from PerCPU.ExceptionStackTop
	// PerCPU layout:
	//   0:  currentThread (8)
	//   8:  TopHalfIRQNum (8)
	//   16: NeedsAsyncPreempt (4)
	//   20: NeedsThreadPreempt (4)
	//   24: LocalTickCounter (8)
	//   32: G0StackTop (8)
	//   40: G0StackBottom (8)
	//   48: ExceptionStackTop (8)
	//   56: ExceptionStackBottom (8)
	//   64: Online (4)
	//   68: _padding (4)

	// Load G0StackTop for SP_EL0
	MOVD	32(R20), R1		// R1 = G0StackTop

	// Load ExceptionStackTop for SP_EL1
	MOVD	48(R20), R2		// R2 = ExceptionStackTop

	// Check if stacks are allocated
	CBZ	R1, stack_not_ready
	CBZ	R2, stack_not_ready

	// Set SP_EL0 (will be used in EL1t mode)
	MSR	R1, SP_EL0

	// Set SP_EL1 for exception handling
	MSR	R2, SP_EL1

	// Switch to EL1t mode (use SP_EL0)
	MSR	$0, SPSel

	// ========================================
	// Step 4: Enable FPU (CPACR_EL1)
	// ========================================
	// Enable SIMD/FP: set FPEN bits [21:20] to 0b11
	MOVD	$(3 << 20), R1
	MSR	R1, CPACR_EL1
	ISB	$15

	// ========================================
	// Step 5: Install exception vectors
	// ========================================
	// Use the same exception vectors as CPU 0
	MOVD	$·ExceptionVectorTable(SB), R1
	MSR	R1, VBAR_EL1
	ISB	$15

	// ========================================
	// Step 6: Disable alignment checking
	// ========================================
	// Go runtime requires unaligned access
	MRS	SCTLR_EL1, R1
	BIC	$2, R1, R1		// Clear A bit (alignment check)
	MSR	R1, SCTLR_EL1
	ISB	$15

	// ========================================
	// Step 7: Initialize GIC CPU Interface (GICC)
	// ========================================
	// CRITICAL: Each CPU has its own banked GICC registers that must be
	// initialized before enabling IRQs. Without this, the CPU cannot
	// properly acknowledge or mask interrupts, which can break the
	// entire GIC subsystem.
	//
	// GICC registers (at high-memory address 0xFFFFFFFF08010000):
	//   GICC_CTLR (0x000) = 0x01: Enable Group 0 interrupts
	//   GICC_PMR  (0x004) = 0xFF: Allow all priority levels
	//   GICC_BPR  (0x008) = 0x00: Binary point (matches CPU 0)
	//
	MOVD	$0xFFFFFFFF08010000, R4	// R4 = GICC base (high memory)

	// Set priority mask to allow all interrupts (GICC_PMR)
	MOVW	$0xFF, R1
	MOVW	R1, 4(R4)		// GICC_PMR = 0xFF

	// Set binary point (GICC_BPR)
	MOVW	$0, R1
	MOVW	R1, 8(R4)		// GICC_BPR = 0

	// Enable CPU interface for Group 0 (GICC_CTLR)
	MOVW	$1, R1
	MOVW	R1, (R4)		// GICC_CTLR = 0x01

	// Barrier to ensure GICC writes complete before enabling IRQs
	DSB	$15
	ISB	$15

	// ========================================
	// Step 8: Mark CPU as online
	// ========================================
	// Calculate address of Online field (offset 64 from R20)
	ADD	$64, R20, R3		// R3 = address of Online field
	MOVD	$1, R1
	STLRW	R1, (R3)		// Store with release semantics to Online field

	// ========================================
	// Step 9: Enable interrupts and enter idle loop
	// ========================================
	// Enable IRQs
	MSR	$0x2, DAIFClr		// Clear I bit to enable IRQs

secondary_idle_loop:
	// Wait for interrupt (low power state)
	HINT	$1			// WFI

	// After waking, loop back
	B	secondary_idle_loop

stack_not_ready:
	// Stacks not yet allocated - wait and retry
	// Simple delay
	MOVD	$0x100000, R1
delay_loop:
	SUB	$1, R1, R1
	CBNZ	R1, delay_loop

	// Jump back to try loading stacks again
	B	·secondaryCPUEntry(SB)

// ============================================================================
// getSecondaryCPUEntryAddr returns the address of secondaryCPUEntry
// for use with PSCI CPU_ON
// ============================================================================
// func getSecondaryCPUEntryAddr() uintptr
TEXT ·getSecondaryCPUEntryAddr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	$·secondaryCPUEntry(SB), R0
	MOVD	R0, ret+0(FP)
	RET
