#include "textflag.h"

// lib_barriers.s - Memory barrier and synchronization functions in Go/Plan9 assembly
// These provide critical synchronization primitives for ARM64
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

// dsb() - Data Synchronization Barrier (System-wide)
// Ensures all memory accesses before this instruction complete before continuing
// Used after modifying page tables, before enabling MMU, etc.
TEXT dsb(SB), NOSPLIT|NOFRAME, $0-0
	DSB	$15			// DSB SY (system-wide barrier)
	RET

// dsbIsh() - Data Synchronization Barrier (Inner Shareable)
// Like DSB but only waits for inner-shareable domain operations
// This is what ARM Trusted Firmware uses for MMU enable sequence
// DSB ISH = DSB $11 (0b1011 = Inner Shareable, load+store)
TEXT dsbIsh(SB), NOSPLIT|NOFRAME, $0-0
	DSB	$11			// DSB ISH (inner shareable barrier)
	RET

// dsbIshst() - Data Synchronization Barrier (Inner Shareable, Store-only)
// Like DSB ISH but only for store operations
// Used before modifying page table entries
// DSB ISHST = DSB $10 (0b1010 = Inner Shareable, store-only)
TEXT dsbIshst(SB), NOSPLIT|NOFRAME, $0-0
	DSB	$10			// DSB ISHST (inner shareable store barrier)
	RET

// isb() - Instruction Synchronization Barrier
// Flushes the pipeline and ensures all instructions before this barrier
// complete before any instructions after it begin execution
// Used after modifying system registers, exception vectors, etc.
TEXT isb(SB), NOSPLIT|NOFRAME, $0-0
	ISB	$15			// ISB SY
	RET

// get_stack_pointer() uintptr
// Returns the current stack pointer value
// Returns: R0 = stack pointer value (uintptr)
TEXT get_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	RSP, R0			// Move stack pointer to R0
	// Return value is in R0 (register ABI)
	RET

// set_stack_pointer(sp uintptr)
// Sets the stack pointer register to a new value
// WARNING: Must be 16-byte aligned on ARM64
// Parameters: R0 = new stack pointer value (uintptr)
TEXT set_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// R0 = new stack pointer (already in R0 from register ABI)
	// Check alignment (SP must be 16-byte aligned)
	AND	$0xF, R0, R1		// R1 = R0 & 0xF (lower 4 bits)
	CBNZ	R1, sp_misaligned	// If not zero, SP is misaligned
	// SP is aligned, set it
	MOVD	R0, RSP			// Set stack pointer
	DSB	$15			// Memory barrier
	RET

sp_misaligned:
	// Round down to 16-byte boundary and continue
	AND	$~0xF, R0, R0		// Clear lower 4 bits
	MOVD	R0, RSP			// Set aligned SP
	DSB	$15			// Memory barrier
	RET

// set_g_pointer(gptr uintptr)
// Sets x28 (g pointer register) to a new goroutine pointer
// Parameters: R0 = new goroutine pointer (uintptr)
TEXT set_g_pointer(SB), NOSPLIT|NOFRAME, $0-8
	// R0 = new g pointer (already in R0 from register ABI)
	MOVD	R0, g			// g is alias for R28
	DSB	$15			// Memory barrier
	RET

// get_current_g() uintptr
// Returns pointer to current goroutine from x28 register
// Returns: R0 = current g pointer (uintptr)
TEXT get_current_g(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	g, R0			// g is alias for R28
	// Return value is in R0 (register ABI)
	RET

// set_current_g(gptr uintptr)
// Sets the current goroutine pointer in x28 register
// Parameters: R0 = pointer to goroutine structure (uintptr)
TEXT set_current_g(SB), NOSPLIT|NOFRAME, $0-8
	// R0 = new g pointer (already in R0 from register ABI)
	MOVD	R0, g			// Set g register (R28)
	RET

// Wfi() - Wait For Interrupt
// Puts the CPU in low-power mode until an interrupt arrives
TEXT Wfi(SB), NOSPLIT|NOFRAME, $0-0
	HINT	$1			// WFI instruction
	RET

// Nop() - No operation
// Simple NOP instruction for busy-wait loops
TEXT Nop(SB), NOSPLIT|NOFRAME, $0-0
	NOOP				// NOP instruction
	RET
