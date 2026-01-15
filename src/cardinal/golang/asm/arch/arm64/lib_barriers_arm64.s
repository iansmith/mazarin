#include "textflag.h"

// lib_barriers.s - Memory barrier and synchronization functions in Go/Plan9 assembly
// These provide critical synchronization primitives for ARM64
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains atomic primitives that cannot be decomposed further.
// Each function is a single-purpose operation that must execute as inline
// assembly due to hardware requirements (barriers, register access, etc.).
//
// Functions:
//   - dsb()          - Full system data barrier
//   - dsbIsh()       - Inner shareable data barrier
//   - dsbIshst()     - Inner shareable store barrier
//   - isb()          - Instruction synchronization barrier
//   - get_stack_pointer() - Read SP register
//   - set_stack_pointer() - Write SP register with alignment check
//   - set_g_pointer() - Set g register (R28) with barrier
//   - get_current_g() - Read g register (R28)
//   - set_current_g() - Set g register (R28) without barrier
//   - Wfi()          - Wait for interrupt (low-power)
//   - Nop()          - No operation (busy-wait)
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

// ============================================================================
// dsb() - Data Synchronization Barrier (System-wide)
// ============================================================================
// Ensures all memory accesses before this instruction complete before continuing
// Used after modifying page tables, before enabling MMU, etc.
//
// Segments:
//   1. Issue DSB SY barrier instruction
//   2. Return to caller
//
TEXT dsb(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Issue DSB SY barrier
	DSB	$15			// DSB SY (system-wide barrier, $15 = 0b1111)

	// Segment 2: Return
	RET

// ============================================================================
// dsbIsh() - Data Synchronization Barrier (Inner Shareable)
// ============================================================================
// Like DSB but only waits for inner-shareable domain operations
// This is what ARM Trusted Firmware uses for MMU enable sequence
// DSB ISH = DSB $11 (0b1011 = Inner Shareable, load+store)
//
// Segments:
//   1. Issue DSB ISH barrier instruction
//   2. Return to caller
//
TEXT dsbIsh(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Issue DSB ISH barrier
	DSB	$11			// DSB ISH (inner shareable barrier, $11 = 0b1011)

	// Segment 2: Return
	RET

// ============================================================================
// dsbIshst() - Data Synchronization Barrier (Inner Shareable, Store-only)
// ============================================================================
// Like DSB ISH but only for store operations
// Used before modifying page table entries
// DSB ISHST = DSB $10 (0b1010 = Inner Shareable, store-only)
//
// Segments:
//   1. Issue DSB ISHST barrier instruction
//   2. Return to caller
//
TEXT dsbIshst(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Issue DSB ISHST barrier
	DSB	$10			// DSB ISHST (inner shareable store barrier, $10 = 0b1010)

	// Segment 2: Return
	RET

// ============================================================================
// isb() - Instruction Synchronization Barrier
// ============================================================================
// Flushes the pipeline and ensures all instructions before this barrier
// complete before any instructions after it begin execution
// Used after modifying system registers, exception vectors, etc.
//
// Segments:
//   1. Issue ISB barrier instruction
//   2. Return to caller
//
TEXT isb(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Issue ISB barrier
	ISB	$15			// ISB SY (instruction barrier, $15 = 0b1111)

	// Segment 2: Return
	RET

// ============================================================================
// get_stack_pointer() - Read Stack Pointer Register
// ============================================================================
// Returns the current stack pointer value
// Returns: R0 = stack pointer value (uintptr)
//
// Segments:
//   1. Copy SP to R0 (return value)
//   2. Return to caller
//
TEXT get_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Copy SP to R0
	MOVD	RSP, R0			// Move stack pointer to R0 (return register)

	// Segment 2: Return
	RET

// ============================================================================
// set_stack_pointer(sp uintptr) - Write Stack Pointer Register
// ============================================================================
// Sets the stack pointer register to a new value
// WARNING: Must be 16-byte aligned on ARM64
// Parameters: R0 = new stack pointer value (uintptr)
//
// Segments:
//   1. Check alignment (SP must be 16-byte aligned)
//   2. Set SP and issue barrier (aligned path)
//   3. Round down and set SP (misaligned path)
//
TEXT set_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Check alignment
	// R0 = new stack pointer (already in R0 from register ABI)
	AND	$0xF, R0, R1		// R1 = R0 & 0xF (extract lower 4 bits)
	CBNZ	R1, sp_misaligned	// If not zero, SP is misaligned

	// Segment 2: Set SP (aligned path)
	MOVD	R0, RSP			// Set stack pointer
	DSB	$15			// Memory barrier to ensure visibility
	RET

sp_misaligned:
	// Segment 3: Round down and set SP (misaligned path)
	AND	$~0xF, R0, R0		// Clear lower 4 bits (round down to 16-byte)
	MOVD	R0, RSP			// Set aligned SP
	DSB	$15			// Memory barrier
	RET

// ============================================================================
// set_g_pointer(gptr uintptr) - Set g Register with Barrier
// ============================================================================
// Sets x28 (g pointer register) to a new goroutine pointer
// Parameters: R0 = new goroutine pointer (uintptr)
// NOTE: Includes DSB barrier - use for context switches requiring visibility
//
// Segments:
//   1. Set g register (R28)
//   2. Issue memory barrier
//   3. Return to caller
//
TEXT set_g_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Set g register
	// R0 = new g pointer (already in R0 from register ABI)
	MOVD	R0, g			// g is alias for R28

	// Segment 2: Issue memory barrier
	DSB	$15			// Ensure g change is visible

	// Segment 3: Return
	RET

// ============================================================================
// get_current_g() - Read g Register
// ============================================================================
// Returns pointer to current goroutine from x28 register
// Returns: R0 = current g pointer (uintptr)
//
// Segments:
//   1. Copy g register (R28) to R0 (return value)
//   2. Return to caller
//
TEXT get_current_g(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Copy g to R0
	MOVD	g, R0			// g is alias for R28, copy to return register

	// Segment 2: Return
	RET

// ============================================================================
// set_current_g(gptr uintptr) - Set g Register without Barrier
// ============================================================================
// Sets the current goroutine pointer in x28 register
// Parameters: R0 = pointer to goroutine structure (uintptr)
// NOTE: No DSB barrier - use for simple g updates without context switch
//
// Segments:
//   1. Set g register (R28)
//   2. Return to caller
//
TEXT set_current_g(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Set g register
	// R0 = new g pointer (already in R0 from register ABI)
	MOVD	R0, g			// Set g register (R28)

	// Segment 2: Return
	RET

// ============================================================================
// Wfi() - Wait For Interrupt
// ============================================================================
// Puts the CPU in low-power mode until an interrupt arrives
// Used in idle loops to save power
//
// Segments:
//   1. Issue WFI instruction
//   2. Return to caller (when interrupt wakes CPU)
//
TEXT Wfi(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Issue WFI instruction
	HINT	$1			// WFI instruction (hint opcode 1)

	// Segment 2: Return
	RET

// ============================================================================
// Nop() - No Operation
// ============================================================================
// Simple NOP instruction for busy-wait loops or timing
// Does nothing but consume one CPU cycle
//
// Segments:
//   1. Execute NOP instruction
//   2. Return to caller
//
TEXT Nop(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Execute NOP
	NOOP				// NOP instruction

	// Segment 2: Return
	RET
