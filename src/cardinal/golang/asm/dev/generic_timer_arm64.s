// lib_timer.s - ARM Generic Timer access functions in Go/Plan9 assembly
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains atomic timer primitives that cannot be decomposed further.
// Each function is a single-purpose operation accessing ARM64 timer registers.
//
// IMPORTANT: Using VIRTUAL timer (CNTV_*) at EL1 - matches reference repo!
// Virtual timer is the standard choice for EL1 OS/kernel code.
//
// Functions are organized by category:
//   - Virtual Timer (CNTV_*): Primary timer for EL1 kernel (PPI 27)
//     * Control: read_cntv_ctl_el0, write_cntv_ctl_el0
//     * Timer Value: read_cntv_tval_el0, write_cntv_tval_el0
//     * Compare Value: read_cntv_cval_el0, write_cntv_cval_el0
//     * Counter: read_cntvct_el0
//     * Frequency: read_cntfrq_el0
//
//   - Physical Timer (CNTP_*): For comparison with virtual timer (PPI 30)
//     * Control: read_cntp_ctl_el0, write_cntp_ctl_el0
//     * Timer Value: read_cntp_tval_el0, write_cntp_tval_el0
//     * Compare Value: read_cntp_cval_el0, write_cntp_cval_el0
//     * Counter: read_cntpct_el0
//
// ABI NOTES:
// - These are abi0 functions. Go generates wrappers to call them from internal ABI.
// - Arguments: The wrapper passes args on stack. Read from value+0(FP).
// - Return values: MUST be stored to ret+0(FP). The wrapper reads from stack, not R0.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

// ============================================================================
// Virtual Timer (CNTV_*) - Primary timer for EL1 kernel
// Virtual timer uses PPI 27
// ============================================================================

// ============================================================================
// read_cntv_ctl_el0() - Read Virtual Timer Control Register
// ============================================================================
// Read CNTV_CTL_EL0 (Virtual Timer Control Register)
// Returns: control register value (uint32)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTV_CTL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntv_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CNTV_CTL_EL0
	MRS	CNTV_CTL_EL0, R0

	// Segment 2: Store to stack
	MOVW	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntv_ctl_el0(value uint32) - Write Virtual Timer Control Register
// ============================================================================
// Write CNTV_CTL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTV_CTL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntv_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Load from stack
	MOVW	value+0(FP), R0

	// Segment 2: Write to CNTV_CTL_EL0
	MSR	R0, CNTV_CTL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntv_tval_el0() - Read Virtual Timer Value Register
// ============================================================================
// Read CNTV_TVAL_EL0 (Virtual Timer Value Register)
// Returns: timer value (uint32)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTV_TVAL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntv_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CNTV_TVAL_EL0
	MRS	CNTV_TVAL_EL0, R0

	// Segment 2: Store to stack
	MOVW	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntv_tval_el0(value uint32) - Write Virtual Timer Value Register
// ============================================================================
// Write CNTV_TVAL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTV_TVAL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntv_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Load from stack
	MOVW	value+0(FP), R0

	// Segment 2: Write to CNTV_TVAL_EL0
	MSR	R0, CNTV_TVAL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntv_cval_el0() - Read Virtual Timer Compare Value Register
// ============================================================================
// Read CNTV_CVAL_EL0 (Virtual Timer Compare Value Register)
// Returns: compare value (uint64)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTV_CVAL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntv_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read CNTV_CVAL_EL0
	MRS	CNTV_CVAL_EL0, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntv_cval_el0(value uint64) - Write Virtual Timer Compare Value Register
// ============================================================================
// Write CNTV_CVAL_EL0
// Parameters: value at stack offset 0 (uint64)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTV_CVAL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntv_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load from stack
	MOVD	value+0(FP), R0

	// Segment 2: Write to CNTV_CVAL_EL0
	MSR	R0, CNTV_CVAL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntvct_el0() - Read Virtual Counter Register
// ============================================================================
// Read CNTVCT_EL0 (Virtual Counter Register)
// Returns: current counter value (uint64)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTVCT_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntvct_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read CNTVCT_EL0
	MRS	CNTVCT_EL0, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// read_cntfrq_el0() - Read Counter Frequency Register
// ============================================================================
// Read CNTFRQ_EL0 (Counter Frequency Register)
// Returns: timer frequency in Hz (uint32)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTFRQ_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntfrq_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CNTFRQ_EL0
	MRS	CNTFRQ_EL0, R0

	// Segment 2: Store to stack
	MOVW	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// Physical Timer (CNTP_*) - For comparison with virtual timer
// Physical timer uses PPI 30
// ============================================================================

// ============================================================================
// read_cntp_ctl_el0() - Read Physical Timer Control Register
// ============================================================================
// Read CNTP_CTL_EL0 (Physical Timer Control Register)
// Returns: control register value (uint32)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTP_CTL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntp_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CNTP_CTL_EL0
	MRS	CNTP_CTL_EL0, R0

	// Segment 2: Store to stack
	MOVW	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntp_ctl_el0(value uint32) - Write Physical Timer Control Register
// ============================================================================
// Write CNTP_CTL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTP_CTL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntp_ctl_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Load from stack
	MOVW	value+0(FP), R0

	// Segment 2: Write to CNTP_CTL_EL0
	MSR	R0, CNTP_CTL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntp_tval_el0() - Read Physical Timer Value Register
// ============================================================================
// Read CNTP_TVAL_EL0 (Physical Timer Value Register)
// Returns: timer value (uint32)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTP_TVAL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntp_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CNTP_TVAL_EL0
	MRS	CNTP_TVAL_EL0, R0

	// Segment 2: Store to stack
	MOVW	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntp_tval_el0(value uint32) - Write Physical Timer Value Register
// ============================================================================
// Write CNTP_TVAL_EL0
// Parameters: value at stack offset 0 (uint32)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTP_TVAL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntp_tval_el0(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Load from stack
	MOVW	value+0(FP), R0

	// Segment 2: Write to CNTP_TVAL_EL0
	MSR	R0, CNTP_TVAL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntp_cval_el0() - Read Physical Timer Compare Value Register
// ============================================================================
// Read CNTP_CVAL_EL0 (Physical Timer Compare Value Register)
// Returns: compare value (uint64)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTP_CVAL_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntp_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read CNTP_CVAL_EL0
	MRS	CNTP_CVAL_EL0, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// write_cntp_cval_el0(value uint64) - Write Physical Timer Compare Value Register
// ============================================================================
// Write CNTP_CVAL_EL0
// Parameters: value at stack offset 0 (uint64)
// ABI0: Must read argument from stack
//
// Segments:
//   1. Load value from stack to R0
//   2. Write R0 to CNTP_CVAL_EL0
//   3. Issue instruction barrier
//   4. Return to caller
//
TEXT write_cntp_cval_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load from stack
	MOVD	value+0(FP), R0

	// Segment 2: Write to CNTP_CVAL_EL0
	MSR	R0, CNTP_CVAL_EL0

	// Segment 3: Barrier
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_cntpct_el0() - Read Physical Counter Register
// ============================================================================
// Read CNTPCT_EL0 (Physical Counter Register)
// Returns: current counter value (uint64)
// ABI0: Must store return value to stack for wrapper to read
//
// Segments:
//   1. Read CNTPCT_EL0 to R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT read_cntpct_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read CNTPCT_EL0
	MRS	CNTPCT_EL0, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET
