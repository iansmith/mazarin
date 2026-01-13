#include "textflag.h"

// goroutine.s - Goroutine Context Switching (Go/Plan9 Assembly)
//
// ============================================================================
// SEGMENT OVERVIEW (see asm/docs/small_files_decomposition.md)
// ============================================================================
//
// switchToGoroutine segments:
//   Segment 1: Set g - Set g register to new goroutine
//   Segment 2: Get Stack - Load stack pointer from g.sched.sp
//   Segment 3: Verify Alignment - Check 16-byte alignment, error if misaligned
//   Segment 4: Switch SP & Return - Set RSP and return to caller
//
// runOnGoroutine segments:
//   Segment 1: Save Caller State - Save callee-saved registers on current stack
//   Segment 2: Set g & Get Stack - Switch g, load new goroutine's stack
//   Segment 3: Execute Function - Call the function on new stack
//   Segment 4: Restore & Return - Restore state and return to caller
//
// callOnG0Stack segments:
//   Segment 1: Save State - Save function pointer and current g
//   Segment 2: Get g0 - Load g0 from current g.m.g0
//   Segment 3: Switch to g0 - Set g, update TLS, switch SP
//   Segment 4: Execute Function - Call function (never returns)
//
// POST-BOOT NOTE: These are context switch operations - must remain inline assembly.
// Stack manipulation and g pointer changes require atomic sequences.
//

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

	// ========================================================================
	// SEGMENT 1: Set g
	// ========================================================================
	// Set g register to new goroutine (CRITICAL for Go runtime).
	MOVD R0, g

	// ========================================================================
	// SEGMENT 2: Get Stack
	// ========================================================================
	// Load g.sched.sp (offset 56 in runtimeG).
	MOVD 56(R0), R2

	// ========================================================================
	// SEGMENT 3: Verify Alignment
	// ========================================================================
	// SP must be 16-byte aligned.
	AND $0xF, R2, R3
	CBNZ R3, sp_misaligned_switch

	// ========================================================================
	// SEGMENT 4: Switch SP & Return
	// ========================================================================
	MOVD R2, RSP
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
	// ========================================================================
	// SEGMENT 1: Save Caller State
	// ========================================================================
	// Save callee-saved registers on current stack.
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
	MOVD RSP, R19                    // Save current RSP
	MOVD g, R20                      // Save old g pointer
	MOVD R1, R21                     // Save function pointer

	// ========================================================================
	// SEGMENT 2: Set g & Get Stack
	// ========================================================================
	MOVD R0, g                       // Set g to new goroutine
	MOVD 56(R0), R2                  // Get g.sched.sp (offset 56)
	AND $0xF, R2, R3
	CBNZ R3, run_sp_misaligned
	MOVD R2, RSP                     // Switch to new goroutine's stack

	// ========================================================================
	// SEGMENT 3: Execute Function
	// ========================================================================
	// In Go, func() is a funcval struct where first word is code pointer.
	MOVD (R21), R3
	CALL (R3)

	// ========================================================================
	// SEGMENT 4: Restore & Return
	// ========================================================================
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

	// ========================================================================
	// SEGMENT 1: Save State
	// ========================================================================
	// Save function pointer and current g.
	MOVD R0, R19                     // Save function pointer
	MOVD g, R20                      // Save current g

	// ========================================================================
	// SEGMENT 2: Get g0
	// ========================================================================
	// Get g0 from g.m.g0. g.m is at offset 48, m.g0 at offset 0.
	MOVD 48(g), R21                  // R21 = g.m
	MOVD 0(R21), R22                 // R22 = m.g0

	// ========================================================================
	// SEGMENT 3: Switch to g0
	// ========================================================================
	// Set g to g0, update TLS, switch to g0's stack.
	MOVD R22, g
	CALL runtime·save_g·abi0(SB)     // Update TLS
	MOVD 56(R22), R23                // R23 = g0.sched.sp
	AND $0xF, R23, R24
	CBNZ R24, g0_sp_misaligned
	MOVD R23, RSP                    // Switch to g0's stack

	// ========================================================================
	// SEGMENT 4: Execute Function
	// ========================================================================
	// Call function (never returns - should call schedule()).
	MOVD (R19), R24
	CALL (R24)

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
