#include "textflag.h"

// lib_barriers.s - Memory barrier and synchronization functions in Go/Plan9 assembly
// These provide critical synchronization primitives for ARM64

// dsb() - Data Synchronization Barrier
// Ensures all memory accesses before this instruction complete before continuing
// Used after modifying page tables, before enabling MMU, etc.
TEXT dsb(SB), NOSPLIT|NOFRAME, $0-0
	DSB	$15			// DSB SY (system-wide barrier)
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
// Useful for debugging and stack management
TEXT get_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	RSP, R0			// Move stack pointer to R0
	MOVD	R0, ret+0(FP)		// Return value
	RET

// set_stack_pointer(sp uintptr)
// Sets the stack pointer register to a new value
// WARNING: Must be 16-byte aligned on ARM64
// Parameters:
//   sp+0(FP): new stack pointer value (uintptr)
TEXT set_stack_pointer(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	sp+0(FP), R0		// R0 = new stack pointer
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
// Parameters:
//   gptr+0(FP): new goroutine pointer (uintptr)
TEXT set_g_pointer(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	gptr+0(FP), R0		// R0 = new g pointer
	MOVD	R0, g			// g is alias for R28
	DSB	$15			// Memory barrier
	RET

// get_current_g() uintptr
// Returns pointer to current goroutine from x28 register
TEXT get_current_g(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	g, R0			// g is alias for R28
	MOVD	R0, ret+0(FP)		// Return value
	RET

// set_current_g(gptr uintptr)
// Sets the current goroutine pointer in x28 register
// Parameters:
//   gptr+0(FP): pointer to goroutine structure (uintptr)
TEXT set_current_g(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	gptr+0(FP), R0		// R0 = new g pointer
	MOVD	R0, g			// Set g register (R28)
	RET

// getCurrentSP() uintptr
// Returns the current stack pointer (alternate name for compatibility)
TEXT getCurrentSP(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	RSP, R0			// Copy stack pointer to R0
	MOVD	R0, ret+0(FP)		// Return value
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
