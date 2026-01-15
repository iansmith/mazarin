#include "textflag.h"

// lib_getters_arm64.s - Simple Register and Function Call Wrappers
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains minimal wrapper functions for accessing goroutine state
// and calling runtime initialization functions.
//
// Functions:
//   - getGRegister() - Returns current goroutine pointer (g register)
//   - call_mallocinit() - Calls runtime.mallocinit to initialize heap allocator
//
// WHY NOT DECOMPOSE:
// These functions are already atomic primitives:
//   - getGRegister: 2 instructions (read register + return)
//   - call_mallocinit: Minimal call wrapper (frame setup + call + frame teardown)
// Both perform single well-defined operations that cannot be meaningfully simplified.
//
// NOTE: Functions that reference Go runtime symbols (runtime.g0, runtime.m0,
// runtime.physPageSize, etc.) have been moved to linker_symbols.s (GCC assembly)
// because goasm2gnu cannot properly handle external symbol relocations.
//
// ABI NOTES:
// - getGRegister: Register-based ABI (returns in R0)
// - call_mallocinit: Standard Go function call convention

// ============================================================================
// getGRegister() - Get Current Goroutine Pointer
// ============================================================================
// Returns the current value of the g register (R28)
// Used for debugging and goroutine state inspection
//
// Returns: R0 = current goroutine pointer (g)
//
// Segments:
//   1. Read g register to R0
//   2. Return to caller
//
TEXT getGRegister(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read g register
	MOVD	g, R0			// R28 (g) → R0

	// Segment 2: Return
	RET

// ============================================================================
// call_mallocinit() - Initialize Runtime Heap Allocator
// ============================================================================
// Calls runtime.mallocinit to initialize the Go heap allocator
// This is a minimal wrapper that initializes only the allocator, not the full scheduler
//
// Prerequisites: physPageSize must be set from Go before calling
//
// Segments:
//   1. Save frame pointer and link register to stack
//   2. Adjust stack pointer and set up frame pointer
//   3. Call runtime.mallocinit
//   4. Restore frame pointer and link register
//   5. Adjust stack pointer back
//   6. Return to caller
//
TEXT call_mallocinit(SB), NOSPLIT, $0-0
	// Segment 1: Save frame pointer and link register
	MOVD	R29, -16(RSP)
	MOVD	R30, -8(RSP)

	// Segment 2: Set up stack frame
	SUB	$16, RSP
	MOVD	RSP, R29

	// Segment 3: Call runtime.mallocinit
	// This initializes just the heap allocator, not full scheduler
	CALL	runtime·mallocinit(SB)

	// Segment 4: Restore frame pointer and link register
	MOVD	0(RSP), R29
	MOVD	8(RSP), R30

	// Segment 5: Restore stack pointer
	ADD	$16, RSP

	// Segment 6: Return
	RET
