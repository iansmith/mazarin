// lib_timer.s - ARM Generic Timer access functions in Go/Plan9 assembly
//
// This file contains functions for accessing ARM64 timer registers.
// IMPORTANT: Using VIRTUAL timer (CNTV_*) at EL1 - matches reference repo!
// Virtual timer is the standard choice for EL1 OS/kernel code.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

// ============================================================================
// Virtual Timer (CNTV_*) - Primary timer for EL1 kernel
// Virtual timer uses PPI 27
// ============================================================================

// read_cntv_ctl_el0() uint32
// Read CNTV_CTL_EL0 (Virtual Timer Control Register)
// Returns: control register value (uint32)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntv_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CNTV_CTL_EL0, R0
	MOVW	R0, ret+0(FP)
	RET

// write_cntv_ctl_el0(value uint32)
// Write CNTV_CTL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
TEXT write_cntv_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	MOVW	value+0(FP), R0
	MSR	R0, CNTV_CTL_EL0
	ISB	$15
	RET

// read_cntv_tval_el0() uint32
// Read CNTV_TVAL_EL0 (Virtual Timer Value Register)
// Returns: timer value (uint32)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntv_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CNTV_TVAL_EL0, R0
	MOVW	R0, ret+0(FP)
	RET

// write_cntv_tval_el0(value uint32)
// Write CNTV_TVAL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
TEXT write_cntv_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	MOVW	value+0(FP), R0
	MSR	R0, CNTV_TVAL_EL0
	ISB	$15
	RET

// read_cntv_cval_el0() uint64
// Read CNTV_CVAL_EL0 (Virtual Timer Compare Value Register)
// Returns: compare value (uint64)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntv_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	CNTV_CVAL_EL0, R0
	MOVD	R0, ret+0(FP)
	RET

// write_cntv_cval_el0(value uint64)
// Write CNTV_CVAL_EL0
// Parameters: value at stack offset 0 (uint64)
// ABI0: Must read argument from stack
TEXT write_cntv_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	value+0(FP), R0
	MSR	R0, CNTV_CVAL_EL0
	ISB	$15
	RET

// read_cntvct_el0() uint64
// Read CNTVCT_EL0 (Virtual Counter Register)
// Returns: current counter value (uint64)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntvct_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	CNTVCT_EL0, R0
	MOVD	R0, ret+0(FP)
	RET

// read_cntfrq_el0() uint32
// Read CNTFRQ_EL0 (Counter Frequency Register)
// Returns: timer frequency in Hz (uint32)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntfrq_el0(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CNTFRQ_EL0, R0
	MOVW	R0, ret+0(FP)
	RET

// ============================================================================
// Physical Timer (CNTP_*) - For comparison with virtual timer
// Physical timer uses PPI 30
// ============================================================================

// read_cntp_ctl_el0() uint32
// Read CNTP_CTL_EL0 (Physical Timer Control Register)
// Returns: control register value (uint32)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntp_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CNTP_CTL_EL0, R0
	MOVW	R0, ret+0(FP)
	RET

// write_cntp_ctl_el0(value uint32)
// Write CNTP_CTL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
TEXT write_cntp_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	MOVW	value+0(FP), R0
	MSR	R0, CNTP_CTL_EL0
	ISB	$15
	RET

// read_cntp_tval_el0() uint32
// Read CNTP_TVAL_EL0 (Physical Timer Value Register)
// Returns: timer value (uint32)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntp_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	MRS	CNTP_TVAL_EL0, R0
	MOVW	R0, ret+0(FP)
	RET

// write_cntp_tval_el0(value uint32)
// Write CNTP_TVAL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
TEXT write_cntp_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	MOVW	value+0(FP), R0
	MSR	R0, CNTP_TVAL_EL0
	ISB	$15
	RET

// read_cntp_cval_el0() uint64
// Read CNTP_CVAL_EL0 (Physical Timer Compare Value Register)
// Returns: compare value (uint64)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntp_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	CNTP_CVAL_EL0, R0
	MOVD	R0, ret+0(FP)
	RET

// write_cntp_cval_el0(value uint64)
// Write CNTP_CVAL_EL0
// Parameters: value at stack offset 0 (uint64)
// ABI0: Must read argument from stack
TEXT write_cntp_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	value+0(FP), R0
	MSR	R0, CNTP_CVAL_EL0
	ISB	$15
	RET

// read_cntpct_el0() uint64
// Read CNTPCT_EL0 (Physical Counter Register)
// Returns: current counter value (uint64)
// ABI0: Must store return value to stack for wrapper to read
TEXT read_cntpct_el0(SB), NOSPLIT|NOFRAME, $0-8
	MRS	CNTPCT_EL0, R0
	MOVD	R0, ret+0(FP)
	RET
