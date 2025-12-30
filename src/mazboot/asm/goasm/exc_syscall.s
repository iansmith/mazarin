// exc_syscall.s - Syscall handling in Go/Plan9 assembly
//
// This file provides the complete syscall handling path:
//   1. handle_svc_syscall - Entry point from sync_restore_and_svc
//   2. syscall_dispatch   - Shuffles args and calls Go SyscallDispatch
//   3. syscall_return     - Restores registers and executes ERET
//   4. load_context_and_eret - Context switch to new thread
//
// Exception frame layout (320 bytes, saved by sync_exception_handler):
//   [SP+0]:   x0, x1        (16 bytes)
//   [SP+16]:  x2, x3        (16 bytes)
//   [SP+32]:  x4, x5        (16 bytes)
//   [SP+48]:  x6, x7        (16 bytes)
//   [SP+64]:  x8, x9        (16 bytes)
//   [SP+80]:  x10, x11      (16 bytes)
//   [SP+96]:  x12, x13      (16 bytes)
//   [SP+112]: x14, x15      (16 bytes)
//   [SP+128]: x16, x17      (16 bytes)
//   [SP+144]: x18, x19      (16 bytes)
//   [SP+160]: x20, x21      (16 bytes)
//   [SP+176]: x22, x23      (16 bytes)
//   [SP+192]: x24, x25      (16 bytes)
//   [SP+208]: x26, x27      (16 bytes)
//   [SP+224]: x28           (8 bytes)
//   [SP+232]: x29, x30      (16 bytes)
//   [SP+248]: original SP   (8 bytes)
//   [SP+256]: ELR_EL1, SPSR_EL1 (16 bytes)
//   [SP+272]: FAR_EL1, ESR_EL1  (16 bytes)
//   [SP+288]: SP_EL0        (8 bytes)
//   [SP+296]: saved g (for syscall) (8 bytes)
//   [SP+304]: saved x0 (for syscall) (8 bytes)
//   [SP+312]: saved LR (for syscall) (8 bytes)

#include "textflag.h"

// Exception frame offsets
#define EXC_FRAME_ELR_SPSR   256
#define EXC_FRAME_SP_EL0     288
#define EXC_FRAME_SAVED_G    296
#define EXC_FRAME_SAVED_X0   304
#define EXC_FRAME_SAVED_LR   312

// ============================================================================
// handle_svc_syscall - Entry point for SVC syscalls
// ============================================================================
// Called from sync_restore_and_svc after registers have been restored.
// Entry state:
//   - R0-R5: Syscall arguments (restored from exception frame)
//   - R8: Syscall number
//   - R28: Original g (saved to exception frame)
//   - R29, R30: Restored FP/LR
//   - SP: Points to exception frame (320 bytes)
//
// This function:
//   1. Disables timer interrupts (prevents async preemption during syscall)
//   2. Sets R28 to runtime.g0 (syscall handlers run on g0)
//   3. Branches to syscall_dispatch
//
TEXT handle_svc_syscall(SB), NOSPLIT, $0
	// CRITICAL: Disable timer interrupts during syscall execution
	// This prevents async preemption from corrupting syscall stack frames
	// We'll re-enable interrupts in syscall_return before eret
	MRS DAIF, R10                  // Read current interrupt mask state
	ORR $0x80, R10, R10            // Set I bit (IRQ mask) to disable timer interrupts
	MSR R10, DAIF                  // Write back to DAIF
	ISB $15                        // Ensure change takes effect before continuing

	// CRITICAL: Switch to g0 before calling Go syscall handlers
	// This allows runtime operations (including stack tracebacks) to work correctly
	// NOTE: Original g (R28) was already saved to exception frame at offset 296
	// by sync_restore_and_svc
	MOVD $runtime·g0(SB), g        // g (R28) = address of runtime.g0 struct

	// NOTE: We don't switch SP to g0's stack here because syscalls need to preserve
	// the caller's stack frame and return properly. We just set R28 so Go code sees g0.
	// Syscall handlers run on the current stack (exception stack), not g0's stack.

	// Fall through to syscall_dispatch
	B syscall_dispatch(SB)

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

// ============================================================================
// syscall_return - Restore registers and return from syscall
// ============================================================================
// Entry state:
//   - R0: Syscall return value (DO NOT restore from frame!)
//   - SP: Points to exception frame (320 bytes)
//
// This function:
//   1. Restores ELR_EL1 and SPSR_EL1 from exception frame
//   2. Restores SP_EL0 from exception frame
//   3. Restores R1-R30 from exception frame (NOT R0 - it's the return value!)
//   4. Deallocates exception frame
//   5. Executes ERET to return to caller
//
// CRITICAL: R0 contains the syscall return value - DO NOT restore it!
// Unlike interrupts (where we restore ALL registers), syscalls use R0 as
// their return value. The syscall handler sets R0, and we preserve it.
//
TEXT syscall_return(SB), NOSPLIT, $0
	// Restore ELR_EL1 and SPSR_EL1 from exception frame
	// Use R12, R13 as temporaries (they'll be restored from frame later)
	LDP EXC_FRAME_ELR_SPSR(RSP), (R12, R13)  // R12 = saved ELR, R13 = saved SPSR

	// NOTE: For SVC exceptions, hardware already sets ELR_EL1 to the instruction
	// AFTER the SVC (PC+4). Do NOT add 4 here - that would skip an instruction!
	// See ARM Architecture Reference Manual: "For exception generating instructions
	// (SVC/HVC/SMC), the preferred return address is the instruction that follows."

	MSR R12, ELR_EL1               // Restore return address (already points to next instruction)
	MSR R13, SPSR_EL1              // Restore saved PSTATE
	ISB $15                        // Ensure ELR/SPSR writes complete

	// Restore SP_EL0 (uses R10 as temporary, will be restored later)
	MOVD EXC_FRAME_SP_EL0(RSP), R10
	MSR R10, SP_EL0
	ISB $15

	// Restore R1-R30 from exception frame (NOT R0 - it's the return value!)
	// Registers are restored in pairs at 16-byte aligned offsets
	LDP 16(RSP), (R2, R3)          // Restore R2, R3
	LDP 32(RSP), (R4, R5)          // Restore R4, R5
	LDP 48(RSP), (R6, R7)          // Restore R6, R7
	LDP 64(RSP), (R8, R9)          // Restore R8, R9
	LDP 80(RSP), (R10, R11)        // Restore R10, R11
	LDP 96(RSP), (R12, R13)        // Restore R12, R13
	LDP 112(RSP), (R14, R15)       // Restore R14, R15
	LDP 128(RSP), (R16, R17)       // Restore R16, R17
	// NOTE: R18 is platform register, not used by Go - skip it
	MOVD 152(RSP), R19             // Restore R19 (skip R18 at offset 144)
	LDP 160(RSP), (R20, R21)       // Restore R20, R21
	LDP 176(RSP), (R22, R23)       // Restore R22, R23
	LDP 192(RSP), (R24, R25)       // Restore R24, R25
	LDP 208(RSP), (R26, R27)       // Restore R26, R27
	// Restore g (R28) from SAVED_G where we saved original g before syscall handler
	MOVD EXC_FRAME_SAVED_G(RSP), g    // Restore g (R28) from offset 296
	LDP 232(RSP), (R29, R30)       // Restore R29 (FP), R30 (LR)
	MOVD 8(RSP), R1                // Restore R1 LAST (after all temp usage above)

	// R0 is NOT restored - it contains the syscall return value!

	// Deallocate exception frame before ERET
	ADD $320, RSP                  // Restore SP_EL1 to exception stack top

	// Return from exception
	// - PC restored from ELR_EL1 (instruction after SVC)
	// - PSTATE restored from SPSR_EL1 (original interrupt state)
	// - R0 = syscall return value (set by handler, preserved here)
	ERET

// ============================================================================
// load_context_and_eret - Load thread context and switch to new thread
// ============================================================================
// Entry: R0 = pointer to ThreadContext struct
//
// ThreadContext layout (from threads.go):
//   X[0-30]:  offsets 0-240  (31 * 8 = 248 bytes)
//   SP:       offset 248
//   ELR:      offset 256
//   SPSR:     offset 264
//
// This routine:
// 1. Loads SP_EL0 from Context.SP
// 2. Loads ELR_EL1 from Context.ELR
// 3. Loads SPSR_EL1 from Context.SPSR
// 4. Loads all general purpose registers R0-R30
// 5. Performs ERET to switch to the new thread
//
TEXT load_context_and_eret(SB), NOSPLIT, $0
	// R0 = pointer to ThreadContext
	MOVD R0, R10                   // Save context pointer in R10

	// Print debug marker 'S' for switch
	MOVD $0x09000000, R11          // UART base
	MOVD $'S', R12
	MOVW R12, (R11)

	// Load SP_EL0 from Context.SP (offset 248)
	MOVD 248(R10), R11
	MSR R11, SP_EL0
	ISB $15

	// Load ELR_EL1 from Context.ELR (offset 256)
	MOVD 256(R10), R11
	MSR R11, ELR_EL1
	ISB $15

	// Load SPSR_EL1 from Context.SPSR (offset 264)
	MOVD 264(R10), R11
	MSR R11, SPSR_EL1
	ISB $15

	// Now load all general purpose registers from Context.X[]
	// We need to be careful - we're using R10 as the context pointer
	// Load R0-R9 first (except R10)
	LDP 0(R10), (R0, R1)           // X[0], X[1]
	LDP 16(R10), (R2, R3)          // X[2], X[3]
	LDP 32(R10), (R4, R5)          // X[4], X[5]
	LDP 48(R10), (R6, R7)          // X[6], X[7]
	LDP 64(R10), (R8, R9)          // X[8], X[9]
	// Skip R10, R11 for now (we're using them)

	// Load R12-R27
	LDP 96(R10), (R12, R13)        // X[12], X[13]
	LDP 112(R10), (R14, R15)       // X[14], X[15]
	LDP 128(R10), (R16, R17)       // X[16], X[17]
	// NOTE: R18 is platform register, not used by Go - skip it
	MOVD 152(R10), R19             // X[19] (skip X[18])
	LDP 160(R10), (R20, R21)       // X[20], X[21]
	LDP 176(R10), (R22, R23)       // X[22], X[23]
	LDP 192(R10), (R24, R25)       // X[24], X[25]
	LDP 208(R10), (R26, R27)       // X[26], X[27]

	// Load g (R28), R29 (FP), R30 (LR)
	MOVD 224(R10), g               // X[28] - Go's g register
	LDP 232(R10), (R29, R30)       // X[29], X[30]

	// Finally load R10 and R11 (last because we were using R10 as base)
	MOVD 88(R10), R11              // X[11]
	MOVD 80(R10), R10              // X[10] - MUST BE LAST (clobbers our base pointer)

	// Reset exception stack pointer to top before ERET
	// This is important - we're leaving the current exception frame behind
	// and starting fresh with the new thread
	// Exception stack cleanup happens on next exception entry.

	// Switch to new thread!
	ERET
