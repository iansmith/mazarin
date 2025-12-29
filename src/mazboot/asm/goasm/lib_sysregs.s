// lib_sysregs.s - System register access functions in Go/Plan9 assembly
//
// This file contains functions for accessing ARM64 system registers.
// These are used for MMU configuration, exception handling, and system control.
//
// IMPORTANT: Go's internal ABI passes arguments in REGISTERS (R0, R1, etc.), not on the stack.
// Functions should use R0 directly for the first argument, not load from FP.
// Return values should be placed in R0, not stored to ret+0(FP).
//
// Migrated from asm/aarch64/lib.s

#include "textflag.h"

// ============================================================================
// SCTLR_EL1 - System Control Register
// ============================================================================

// read_sctlr_el1() uint64
// Read SCTLR_EL1 (System Control Register for EL1)
// Key bits:
//   Bit 0 (M): MMU enable (1=enabled, 0=disabled)
//   Bit 1 (A): Alignment check enable
//   Bit 2 (C): Data cache enable
//   Bit 12 (I): Instruction cache enable
// Returns value in R0 (Go register ABI)
TEXT read_sctlr_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	SCTLR_EL1, R0
	// Return value already in R0 for Go register ABI
	RET

// write_sctlr_el1(value uint64)
// Write SCTLR_EL1
// Argument arrives in R0 (Go register ABI)
TEXT write_sctlr_el1(SB), NOSPLIT, $16-8
	// R0 already contains value from Go register ABI
	// Save LR
	MOVD	LR, R9
	MOVD	R9, 8(RSP)
	// Ensure all prior operations complete
	DSB	$15		// DSB SY
	ISB	$15
	// Invalidate TLB before enabling MMU
	// TLBI VMALLE1 - encoded as: 0xD508871F
	WORD	$0xD508871F
	DSB	$15
	ISB	$15
	// Write SCTLR_EL1 to enable MMU
	MSR	R0, SCTLR_EL1
	ISB	$15
	// Invalidate caches after MMU enable
	WORD	$0xD508751F	// IC IALLU
	// TLBI VMALLE1
	WORD	$0xD508871F
	DSB	$15
	ISB	$15
	// Restore LR
	MOVD	8(RSP), R9
	MOVD	R9, LR
	RET

// enable_mmu_minimal(value uint64)
// Minimal MMU enable function - just enable MMU and return
// Argument arrives in R0 (Go register ABI)
TEXT enable_mmu_minimal(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, SCTLR_EL1
	ISB	$15
	RET

// disable_alignment_check()
// Clear the A bit in SCTLR_EL1 to disable alignment faults
TEXT disable_alignment_check(SB), NOSPLIT|NOFRAME, $0-0
	MRS	SCTLR_EL1, R0
	AND	$~2, R0		// Clear bit 1 (A bit)
	MSR	R0, SCTLR_EL1
	ISB	$15
	RET

// ============================================================================
// CurrentEL and Feature Registers
// ============================================================================

// read_current_el() uint32
// Read CurrentEL (Current Exception Level)
// Returns bits [3:2] containing the exception level
// Returns value in R0 (Go register ABI)
TEXT read_current_el(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CurrentEL, R0
	// Return value already in R0 for Go register ABI
	RET

// read_id_aa64pfr0_el1() uint64
// Read ID_AA64PFR0_EL1 (Processor Feature Register)
// Bits [15:12] = EL3 support, Bits [7:4] = EL1 support, Bits [3:0] = EL0 support
// Returns value in R0 (Go register ABI)
TEXT read_id_aa64pfr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	// MRS ID_AA64PFR0_EL1, X0 - encoded as: 0xD5380400
	WORD	$0xD5380400
	// Return value already in R0 for Go register ABI
	RET

// read_scr_el3() uint64
// Attempt to read SCR_EL3 (Secure Configuration Register)
// NOTE: This will cause a sync exception at EL1 - only callable from EL3
// Returns value in R0 (Go register ABI)
TEXT read_scr_el3(SB), NOSPLIT|NOFRAME, $0-8
	// MRS SCR_EL3, X0 - encoded as: 0xD53E1100
	WORD	$0xD53E1100
	// Return value already in R0 for Go register ABI
	RET

// ============================================================================
// Cache Type Register
// ============================================================================

// read_ctr_el0() uint64
// Read Cache Type Register
// Bit 28 (IDC): Data cache clean NOT required for I/D coherency if 1
// Bit 29 (DIC): Instruction cache invalidation NOT required for I/D coherency if 1
// Returns value in R0 (Go register ABI)
TEXT read_ctr_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	CTR_EL0, R0
	// Return value already in R0 for Go register ABI
	RET

// ============================================================================
// TPIDR Registers (Thread Pointer ID)
// ============================================================================

// read_tpidr_el0() uint64
// Read TPIDR_EL0 (Thread Pointer ID Register, User)
// Used by Go runtime for thread-local storage (TLS)
// Returns value in R0 (Go register ABI)
TEXT read_tpidr_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	TPIDR_EL0, R0
	// Return value already in R0 for Go register ABI
	RET

// write_tpidr_el0(value uint64)
// Write TPIDR_EL0 (Thread Pointer ID Register, User)
// Argument arrives in R0 (Go register ABI)
TEXT write_tpidr_el0(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, TPIDR_EL0
	ISB	$15
	RET

// read_tpidr_el1() uint64
// Read TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// Returns value in R0 (Go register ABI)
TEXT read_tpidr_el1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	TPIDR_EL1, R0
	// Return value already in R0 for Go register ABI
	RET

// write_tpidr_el1(value uint64)
// Write TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// Argument arrives in R0 (Go register ABI)
TEXT write_tpidr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains value from Go register ABI
	MSR	R0, TPIDR_EL1
	ISB	$15
	RET

// ============================================================================
// VBAR_EL1 - Vector Base Address Register
// ============================================================================

// set_vbar_el1_to_addr(addr uintptr)
// Set VBAR_EL1 to specific address
// NOTE: Caller must execute DSB + ISB after this returns
// Argument arrives in R0 (Go register ABI)
TEXT set_vbar_el1_to_addr(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains addr from Go register ABI
	MSR	R0, VBAR_EL1
	RET

// ============================================================================
// Runtime Stack Size Configuration
// ============================================================================

// set_maxstacksize(size uintptr)
// Set runtime.maxstacksize
// Argument arrives in R0 (Go register ABI)
TEXT set_maxstacksize(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains size from Go register ABI
	MOVD	$runtime·maxstacksize(SB), R1
	MOVD	R0, (R1)
	RET

// set_maxstackceiling(size uintptr)
// Set runtime.maxstackceiling
// Argument arrives in R0 (Go register ABI)
TEXT set_maxstackceiling(SB), NOSPLIT|NOFRAME, $0-8
	// R0 already contains size from Go register ABI
	MOVD	$runtime·maxstackceiling(SB), R1
	MOVD	R0, (R1)
	RET
