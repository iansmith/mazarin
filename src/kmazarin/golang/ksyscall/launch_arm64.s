//go:build !test_stubs
// launch_arm64.s - Assembly for userspace transition

#include "textflag.h"

// jumpToUserspace transitions from EL1 to EL0
// func jumpToUserspace(entryPoint, stackPtr uint64)
//
// Sets up:
//   - ELR_EL1 = entry point (where to start executing)
//   - SP_EL0 = stack pointer for userspace
//   - SPSR_EL1 = 0 (EL0 mode, AArch64, all interrupts unmasked)
//
// Then performs ERET to transition to userspace.
//
TEXT ·jumpToUserspace(SB), NOSPLIT|NOFRAME, $0-16
	// Load arguments (while SP is still valid)
	MOVD	entryPoint+0(FP), R0	// R0 = entry point
	MOVD	stackPtr+8(FP), R1	// R1 = stack pointer

	// IMPORTANT: We're in EL1t mode (using SP_EL0 as current SP)
	// Before we can safely set SP_EL0, switch to EL1h mode (SP_EL1)
	// This avoids the issue of MSR SP_EL0 changing our current stack

	// Switch to EL1h mode (use SP_EL1 instead of SP_EL0)
	MOVD	$1, R2
	MSR	R2, SPSel		// SPSel=1 means use SP_EL1

	// Now set ELR_EL1 to the entry point
	MSR	R0, ELR_EL1

	// Now SP_EL0 is not our current SP, so we can safely set it
	MSR	R1, SP_EL0

	// Set SPSR_EL1 for EL0 execution
	// SPSR_EL1 format for returning to EL0:
	//   M[3:0] = 0000 (EL0t - EL0 with SP_EL0)
	//   M[4] = 0 (AArch64)
	//   D, A, I, F = 0 (all exceptions unmasked)
	//   NZCV = 0
	// Value = 0x00000000
	MOVD	$0, R2
	MSR	R2, SPSR_EL1

	// Clear all general-purpose registers for clean start
	// (except what we need for initial state)
	MOVD	$0, R0
	MOVD	$0, R1
	MOVD	$0, R2
	MOVD	$0, R3
	MOVD	$0, R4
	MOVD	$0, R5
	MOVD	$0, R6
	MOVD	$0, R7
	MOVD	$0, R8
	MOVD	$0, R9
	MOVD	$0, R10
	MOVD	$0, R11
	MOVD	$0, R12
	MOVD	$0, R13
	MOVD	$0, R14
	MOVD	$0, R15
	MOVD	$0, R16
	MOVD	$0, R17
	// Skip R18 (platform register)
	// Skip R28 (g register) - will be set by Go runtime
	MOVD	$0, R29	// Frame pointer
	MOVD	$0, R30	// Link register

	// ============================================================
	// Critical pre-ERET sequence (based on Linux kernel entry.S)
	// ============================================================

	// Full TLB invalidation to ensure userspace sees correct page tables
	// TLBI VMALLE1 - Invalidate ALL TLB entries at EL1 (for current VMID)
	WORD	$0xD508871F

	// Data synchronization barrier - ensure TLB invalidation completes
	DSB	$15		// Full system barrier (DSB SY)

	// Speculative unprivileged load workaround (from Linux kernel)
	// TLBI VALE1, XZR - invalidate the zero VA to prevent speculative prefetch
	// Encoding: TLBI VALE1, X0 where X0=0 = 0xD5088760 (but with Xt=xzr = 0xD508877F)
	WORD	$0xD508877F	// TLBI VALE1, XZR
	DSB	$11		// DSB NSH (Non-Shareable domain)

	// Instruction synchronization
	ISB	$15		// Instruction synchronization

	// Return to userspace!
	ERET

	// Speculation Barrier after ERET (from Linux kernel)
	// This prevents speculative execution past ERET
	// SB = DSB SY + ISB (on CPUs without SB instruction)
	DSB	$15
	ISB	$15

	// This should never execute
hang:
	B	hang
