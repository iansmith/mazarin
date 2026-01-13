#include "textflag.h"

// get_caller_sp_arm64.s - Caller Stack Pointer Access
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains a single utility function for obtaining the caller's
// stack pointer value. This is used for debugging and stack introspection.
//
// Function:
//   - get_caller_stack_pointer() - Returns caller's SP value
//
// WHY NOT DECOMPOSE:
// This function is already an atomic primitive (3 instructions). It performs
// a single well-defined operation that cannot be meaningfully broken down further.
//
// ABI NOTES:
// - Package-local function (· prefix) using stack-based ABI
// - Return value stored at ret+0(FP)
// - NOFRAME: Does not establish a stack frame (accesses caller's frame directly)

// ============================================================================
// get_caller_stack_pointer() - Get Caller's Stack Pointer
// ============================================================================
// Returns the caller's stack pointer value by computing from frame pointer
// Stack layout:
//   - Caller's FP and LR are saved at FP+0 and FP+8 (16 bytes)
//   - Caller's frame has 40 byte offset
//   - Therefore: Caller's SP = FP + 56
//
// Returns: ret+0(FP) = caller's SP (uintptr)
//
// Segments:
//   1. Load frame pointer to R0
//   2. Add 56 to compute caller's SP
//   3. Return (R0 automatically copied to ret+0(FP) by ABI wrapper)
//
TEXT ·get_caller_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load frame pointer
	MOVD	R29, R0			// Get caller's frame pointer (x29 → R29)

	// Segment 2: Compute caller's SP
	ADD	$56, R0			// Caller's SP = FP + 56 (16 for saved FP+LR + 40 frame offset)

	// Segment 3: Return
	RET
