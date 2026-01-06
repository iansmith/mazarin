#include "textflag.h"

// Goroutine switching assembly functions
// Simplified versions of Go runtime's gogo() function

// switchToGoroutine switches execution to a new goroutine
// This is called from Go code on g0's stack (the system goroutine)
// This is the proper pattern: g0 switches to user goroutines
// Parameters:
//   R0: Pointer to new goroutine (runtimeG*)
//   R1: Function address to call (uintptr, currently unused - we call kernelMainBodyWrapper directly)
//
// This function:
//   1. Sets g (g pointer) to new goroutine
//   2. Sets RSP to new goroutine's stack
//   3. Sets up call frame and calls kernelMainBodyWrapper
TEXT switchToGoroutine(SB), NOSPLIT, $0
	// R0 = new goroutine pointer (runtimeG*)
	// R1 = function address (currently unused)

	// Set g to new goroutine (CRITICAL for Go runtime)
	// The Go runtime reads the current goroutine via g register
	MOVD R0, g

	// Get new goroutine's stack pointer from g.sched.sp
	// runtimeG layout: stack(16) + stackguard0(8) + stackguard1(8) + _panic(8) + _defer(8) + m(8) = 56
	// sched starts at offset 56
	// sched.sp is at offset 56 + 0 = 56
	MOVD 56(R0), R2  // Load g.sched.sp

	// SP ALIGNMENT CHECK: Verify g.sched.sp is 16-byte aligned before setting RSP
	AND $0xF, R2, R3              // Check alignment (lower 4 bits)
	CBNZ R3, sp_misaligned_switch // If not zero, SP is misaligned!

	// SP is aligned, set it normally
	MOVD R2, RSP

	// Return to caller - they will call the Go function on the new stack
	// The return address is in R30 (link register), not on the stack
	// So we can safely return even though we've switched stacks
	// The Go function will allocate its own frame when called
	RET

sp_misaligned_switch:
	// g.sched.sp was misaligned!
	// Print diagnostic via UART (minimal, no stack)
	MOVD $0x09000000, R3  // UART base

	// Print "SP-MISALIGN: switchToGoroutine g.sched.sp=0x"
	MOVD $0x53, R4  // 'S'
	MOVW R4, (R3)
	MOVD $0x50, R4  // 'P'
	MOVW R4, (R3)
	MOVD $0x2D, R4  // '-'
	MOVW R4, (R3)
	MOVD $0x4D, R4  // 'M'
	MOVW R4, (R3)
	MOVD $0x49, R4  // 'I'
	MOVW R4, (R3)
	MOVD $0x53, R4  // 'S'
	MOVW R4, (R3)
	MOVD $0x41, R4  // 'A'
	MOVW R4, (R3)
	MOVD $0x4C, R4  // 'L'
	MOVW R4, (R3)
	MOVD $0x49, R4  // 'I'
	MOVW R4, (R3)
	MOVD $0x47, R4  // 'G'
	MOVW R4, (R3)
	MOVD $0x3A, R4  // ':'
	MOVW R4, (R3)
	MOVD $0x20, R4  // ' '
	MOVW R4, (R3)
	MOVD $0x73, R4  // 's'
	MOVW R4, (R3)
	MOVD $0x77, R4  // 'w'
	MOVW R4, (R3)
	MOVD $0x69, R4  // 'i'
	MOVW R4, (R3)
	MOVD $0x74, R4  // 't'
	MOVW R4, (R3)
	MOVD $0x63, R4  // 'c'
	MOVW R4, (R3)
	MOVD $0x68, R4  // 'h'
	MOVW R4, (R3)

	// Round down to 16-byte boundary and set SP anyway
	AND $~0xF, R2, R2  // Clear lower 4 bits to align
	MOVD R2, RSP       // Set aligned SP

	// Continue execution (SP is now aligned)
	RET

halt_loop:
	// If kernelMainBodyWrapper returns (shouldn't happen), halt
	WFE
	JMP halt_loop

// runOnGoroutine switches to a new goroutine's stack, runs a function, then returns
// This is used for cooperative goroutine spawning.
//
// Parameters:
//   R0: Pointer to new goroutine (runtimeG*)
//   R1: Function pointer to call (func())
//
// This function:
//   1. Saves caller's state (SP, LR, callee-saved registers)
//   2. Sets g (g pointer) to new goroutine
//   3. Switches RSP to new goroutine's stack
//   4. Calls the function
//   5. Restores original state and returns
//
TEXT runOnGoroutine(SB), NOSPLIT, $0
	// Save callee-saved registers and return address on current stack
	// AArch64 calling convention: R19-g are callee-saved
	// We also save R29 (frame pointer) and R30 (link register)
	SUB $16, RSP
	MOVD R29, 0(RSP)
	MOVD R30, 8(RSP)

	SUB $16, RSP
	MOVD R27, 0(RSP)
	MOVD g, 8(RSP)

	SUB $16, RSP
	MOVD R25, 0(RSP)
	MOVD R26, 8(RSP)

	SUB $16, RSP
	MOVD R23, 0(RSP)
	MOVD R24, 8(RSP)

	SUB $16, RSP
	MOVD R21, 0(RSP)
	MOVD R22, 8(RSP)

	SUB $16, RSP
	MOVD R19, 0(RSP)
	MOVD R20, 8(RSP)

	// Save current RSP in a callee-saved register so we can restore it
	MOVD RSP, R19

	// Save the old g pointer (g) so we can restore it
	MOVD g, R20

	// Save function pointer
	MOVD R1, R21

	// Set g to new goroutine pointer
	MOVD R0, g

	// Get new goroutine's stack pointer from g.sched.sp (offset 56)
	MOVD 56(R0), R2

	// Verify SP is 16-byte aligned
	AND $0xF, R2, R3
	CBNZ R3, run_sp_misaligned

	// Switch to new goroutine's stack
	MOVD R2, RSP

	// Call the function
	// In Go, func() is a pointer to a funcval struct where first word is the code pointer
	MOVD (R21), R3  // Load code pointer from funcval
	CALL (R3)       // Call the function

	// Function returned - restore original state
run_restore:
	// Restore original RSP
	MOVD R19, RSP

	// Restore original g pointer
	MOVD R20, g

	// Restore callee-saved registers
	MOVD 0(RSP), R19
	MOVD 8(RSP), R20
	ADD $16, RSP

	MOVD 0(RSP), R21
	MOVD 8(RSP), R22
	ADD $16, RSP

	MOVD 0(RSP), R23
	MOVD 8(RSP), R24
	ADD $16, RSP

	MOVD 0(RSP), R25
	MOVD 8(RSP), R26
	ADD $16, RSP

	MOVD 0(RSP), R27
	MOVD 8(RSP), g
	ADD $16, RSP

	MOVD 0(RSP), R29
	MOVD 8(RSP), R30
	ADD $16, RSP

	RET

run_sp_misaligned:
	// SP was misaligned - print error via UART and halt
	MOVD $0x09000000, R3
	MOVD $0x47, R4  // 'G'
	MOVW R4, (R3)
	MOVD $0x4F, R4  // 'O'
	MOVW R4, (R3)
	MOVD $0x2D, R4  // '-'
	MOVW R4, (R3)
	MOVD $0x53, R4  // 'S'
	MOVW R4, (R3)
	MOVD $0x50, R4  // 'P'
	MOVW R4, (R3)
	MOVD $0x21, R4  // '!'
	MOVW R4, (R3)
	JMP run_restore  // Try to recover anyway

// callOnG0Stack switches to g0's stack and calls the given function.
// This is similar to runtime.mcall() but simpler - it switches to g0,
// calls the function, and the function must never return (it should call schedule()).
//
// Parameters:
//   R0: Function pointer to call (func())
//
// This function:
//   1. Saves current g register (g)
//   2. Gets g0 from current g.m.g0
//   3. Sets g to g0
//   4. Updates TLS (calls save_g)
//   5. Switches RSP to g0.sched.sp
//   6. Calls the function
//   7. NEVER RETURNS (function calls schedule())
//
TEXT callOnG0Stack(SB), NOSPLIT, $0
	// R0 = function pointer to call

	// Save function pointer for later
	MOVD R0, R19

	// Save current g (in g)
	MOVD g, R20

	// Get current g's m pointer (g.m at offset from runtimeG structure)
	// Using unsafe.Offsetof, g.m is at offset 48
	MOVD 48(g), R21  // R21 = g.m

	// Get m.g0 pointer (m.g0 at offset 0 of runtimeM)
	MOVD 0(R21), R22   // R22 = m.g0

	// Switch g register to g0
	MOVD R22, g      // g = g0

	// Update TLS to point to g0
	// Note: Use .abi0 suffix to match the actual Go runtime symbol name
	CALL runtime·save_g·abi0(SB)

	// Get g0's stack pointer from g0.sched.sp (offset 56)
	MOVD 56(R22), R23  // R23 = g0.sched.sp

	// Verify SP is 16-byte aligned
	AND $0xF, R23, R24
	CBNZ R24, g0_sp_misaligned

	// Switch to g0's stack
	MOVD R23, RSP

	// Call the function (it's a func() closure, first word is code pointer)
	MOVD (R19), R24  // Load code pointer from funcval
	CALL (R24)       // Call function

	// NEVER REACHED - function should call schedule() which never returns
	MOVD $0x09000000, R25
	MOVD $0x3F, R26  // '?'
	MOVW R26, (R25)
never_return_loop:
	JMP never_return_loop  // Hang

g0_sp_misaligned:
	// g0.sched.sp was misaligned!
	MOVD $0x09000000, R25
	MOVD $0x47, R26  // 'G'
	MOVW R26, (R25)
	MOVD $0x30, R26  // '0'
	MOVW R26, (R25)
	MOVD $0x2D, R26  // '-'
	MOVW R26, (R25)
	MOVD $0x53, R26  // 'S'
	MOVW R26, (R25)
	MOVD $0x50, R26  // 'P'
	MOVW R26, (R25)
	MOVD $0x21, R26  // '!'
	MOVW R26, (R25)

	// Round down to 16-byte boundary and continue anyway
	AND $~0xF, R23, R23
	MOVD R23, RSP

	// Try to call function anyway
	MOVD (R19), R24
	CALL (R24)

	// Still shouldn't return
still_never_return_loop:
	JMP still_never_return_loop
