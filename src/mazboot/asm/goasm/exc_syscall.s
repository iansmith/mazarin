// exc_syscall.s - Syscall dispatch entry point (Go/Plan9 assembly)
//
// This file provides a single entry point (syscall_dispatch) that calls
// the Go SyscallDispatch function which handles all syscall routing.
//
// The GCC exception handler calls this function with:
//   - R0-R5: Syscall arguments
//   - R8: Syscall number
//   - SP: Points to exception frame (320 bytes)
//
// Returns:
//   - R0: Syscall result
//   - R1: Context switch flag (-1 = no switch, >=0 = thread index)

#include "textflag.h"

// syscall_dispatch - Central syscall dispatch entry point
// Called from GCC handle_svc_syscall after saving context to exception frame.
//
// Entry:
//   - R0-R5: Syscall arguments (from user)
//   - R8: Syscall number
//   - SP (exception stack): Points to exception frame
//
// The Go function SyscallDispatch takes:
//   - num int64 (R0)
//   - arg0-arg5 uint64 (R1-R6)
//   - framePtr uintptr (R7)
//
// We need to shuffle registers:
//   Old R8 (syscall num) -> R0
//   Old R0 -> R1
//   Old R1 -> R2
//   Old R2 -> R3
//   Old R3 -> R4
//   Old R4 -> R5
//   Old R5 -> R6
//   SP -> R7
//
TEXT syscall_dispatch(SB), NOSPLIT, $0
	// Shuffle syscall args to match Go calling convention
	// Save R0-R5 to scratch area on stack first
	SUB $64, RSP
	STP (R0, R1), 0(RSP)
	STP (R2, R3), 16(RSP)
	STP (R4, R5), 32(RSP)
	MOVD R8, 48(RSP)           // Save syscall number

	// Now set up Go args:
	// R0 = syscall number (was R8)
	// R1 = arg0 (was R0)
	// R2 = arg1 (was R1)
	// R3 = arg2 (was R2)
	// R4 = arg3 (was R3)
	// R5 = arg4 (was R4)
	// R6 = arg5 (was R5)
	// R7 = frame pointer
	//
	// NOTE: Go assembler adds 16-byte prologue, so:
	//   After prologue: SP = exception_frame - 16
	//   After SUB $64: SP = exception_frame - 80
	//   So frame pointer = SP + 80

	MOVD 48(RSP), R0           // R0 = syscall number
	LDP 0(RSP), (R1, R2)       // R1 = old R0, R2 = old R1
	LDP 16(RSP), (R3, R4)      // R3 = old R2, R4 = old R3
	LDP 32(RSP), (R5, R6)      // R5 = old R4, R6 = old R5
	ADD $80, RSP, R7           // R7 = frame pointer (exception frame = RSP + 64 + 16)

	// Restore SP to exception frame
	ADD $64, RSP

	// Call Go dispatch function
	// Need to save callee-saved registers for Go call
	SUB $64, RSP
	STP (R19, R20), 0(RSP)
	STP (R21, R22), 16(RSP)
	STP (R29, R30), 32(RSP)

	CALL main·SyscallDispatch(SB)
	// Returns: R0 = result, R1 = switchTo

	// Restore callee-saved registers
	LDP 0(RSP), (R19, R20)
	LDP 16(RSP), (R21, R22)
	LDP 32(RSP), (R29, R30)
	ADD $64, RSP

	// Check if context switch is needed
	// R0 = result, R1 = switchTo
	CMP $0, R1
	BLT no_switch

	// Context switch needed!
	// Save result in R19 (callee-saved)
	MOVD R0, R19

	// Call DoContextSwitch(framePtr, targetIdx=R1)
	// NOTE: Go assembler added 16-byte prologue, so RSP is 16 bytes below exception frame
	// We need to pass the actual exception frame pointer
	ADD $16, RSP, R0           // R0 = exception frame pointer (RSP + 16 to compensate for prologue)
	// R1 already has targetIdx

	SUB $64, RSP
	STP (R19, R20), 0(RSP)
	STP (R21, R22), 16(RSP)
	STP (R29, R30), 32(RSP)

	CALL main·DoContextSwitch(SB)
	// Returns: R0 = pointer to new ThreadContext

	LDP 0(RSP), (R19, R20)
	LDP 16(RSP), (R21, R22)
	LDP 32(RSP), (R29, R30)
	ADD $64, RSP

	// Load context and switch to new thread
	// NOTE: Go assembler adds prologue that pushes 16 bytes - compensate here
	ADD $16, RSP
	B load_context_and_eret(SB)

no_switch:
	// No context switch - return normally via syscall_return
	// R0 already has the result
	// NOTE: Go assembler adds prologue that pushes 16 bytes - compensate here
	ADD $16, RSP
	B syscall_return(SB)
