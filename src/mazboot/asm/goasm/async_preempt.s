#include "textflag.h"

// async_preempt.s - Bare-metal async preemption for ARM64
// Based on Go runtime's asyncPreempt mechanism
//
// This function is "injected" by the timer interrupt handler.
// The interrupt handler modifies ELR_EL1 to point here, with LR set to interrupted PC.
// This saves all registers, calls the scheduler, then returns to interrupted location.

// UART debug output macro (from exceptions.s)
// CRITICAL: This macro must be used for all debug output, never inline UART writes!
// Expects: R0 contains the character to print (caller-saved)
// Preserves: All registers except R0 (R10 is saved/restored internally)
// Caller must save/restore R0 if needed before/after calling
#define UART_PUTC_SAFE \
	SUB $16, RSP; \
	MOVD R10, 0(RSP); \
	MOVD $0x09000000, R10; \
	MOVW R0, 0(R10); \
	MOVD 0(RSP), R10; \
	ADD $16, RSP

// asyncPreemptBM is the bare-metal equivalent of runtime.asyncPreempt
// Entry state (set by timer interrupt handler):
//   - PC = asyncPreemptBM (via modified ELR_EL1)
//   - LR (R30) = interrupted PC (where to resume after preemption)
//   - SP = interrupted SP - 16 (timer handler allocated 16-byte frame)
//   - [SP+0] = old LR value (saved by timer handler, unused)
//   - [SP+8] = old FP value (saved by timer handler)
//   - All other registers (R0-R29, SIMD, flags) have interrupted values
//
// This function:
//   1. Saves ALL registers including current LR (= interrupted PC)
//   2. Calls timerPreempt() which calls runtime.Gosched()
//   3. When Gosched() returns (we're rescheduled), restores ALL registers
//   4. Returns to LR (interrupted PC)
//
TEXT asyncPreemptBM(SB), NOSPLIT, $0
	// DEBUG: Print 'A' to show asyncPreemptBM was reached
	// Save R0 first since UART_PUTC_SAFE uses it as parameter
	SUB $16, RSP
	MOVD R0, 0(RSP)
	MOVD $'A', R0
	UART_PUTC_SAFE
	MOVD 0(RSP), R0
	ADD $16, RSP

	// Save current LR (= interrupted PC, set by timer handler) BEFORE moving SP
	// We allocate 504 bytes (not 496) to include space for R27
	// ARM64 pre-indexed str has limited range (-256 to +255), so we do this in two steps
	SUB $504, RSP              // SP -= 504
	MOVD R30, 0(RSP)           // Store LR at [SP+0]

	// Set up frame pointer for stack walking
	// Save current FP at [SP-8] (relative to entry SP, now [SP+504-8])
	MOVD R29, 496(RSP)
	ADD $496, RSP, R29         // FP = SP + 496 (points to saved FP location)

	// Save all general-purpose registers (R0-R27, skip g, R29=FP, R30=LR already saved)
	// Use offsets from SP (which now points to saved LR)
	STP (R0, R1), 8(RSP)       // Save R0, R1 at SP+8
	STP (R2, R3), 24(RSP)      // Save R2, R3
	STP (R4, R5), 40(RSP)      // Save R4, R5
	STP (R6, R7), 56(RSP)      // Save R6, R7
	STP (R8, R9), 72(RSP)      // Save R8, R9
	STP (R10, R11), 88(RSP)    // Save R10, R11
	STP (R12, R13), 104(RSP)   // Save R12, R13
	STP (R14, R15), 120(RSP)   // Save R14, R15
	STP (R16, R17), 136(RSP)   // Save R16, R17
	STP (R19, R20), 152(RSP)   // Save R19, R20
	STP (R21, R22), 168(RSP)   // Save R21, R22
	STP (R23, R24), 184(RSP)   // Save R23, R24
	STP (R25, R26), 200(RSP)   // Save R25, R26
	MOVD R27, 216(RSP)         // Save R27 (CRITICAL: we interrupt at arbitrary points, not safe points!)
	// Note: g pointer managed by runtime, updated by Gosched()
	// Note: R29 (FP) already saved above
	// Note: R30 (LR) already saved at SP+0

	// Save condition flags (NZCV)
	MRS NZCV, R0
	MOVD R0, 224(RSP)          // Save NZCV at SP+224

	// Save floating-point status register
	MRS FPSR, R0
	MOVD R0, 232(RSP)          // Save FPSR at SP+232

	// Save all floating-point/SIMD registers (F0-F31, 64-bit view)
	// Use FSTPD to save pairs of 64-bit floating-point registers
	// Note: This saves only 64 bits per register, matching Go runtime's asyncPreempt
	FSTPD (F0, F1), 240(RSP)
	FSTPD (F2, F3), 256(RSP)
	FSTPD (F4, F5), 272(RSP)
	FSTPD (F6, F7), 288(RSP)
	FSTPD (F8, F9), 304(RSP)
	FSTPD (F10, F11), 320(RSP)
	FSTPD (F12, F13), 336(RSP)
	FSTPD (F14, F15), 352(RSP)
	FSTPD (F16, F17), 368(RSP)
	FSTPD (F18, F19), 384(RSP)
	FSTPD (F20, F21), 400(RSP)
	FSTPD (F22, F23), 416(RSP)
	FSTPD (F24, F25), 432(RSP)
	FSTPD (F26, F27), 448(RSP)
	FSTPD (F28, F29), 464(RSP)
	FSTPD (F30, F31), 480(RSP)

	// Now call the Go preemption handler
	// This will call runtime.Gosched() which may switch goroutines
	// When it returns, we're back on this goroutine's stack
	CALL main·timerPreempt(SB)

	// Restore all floating-point/SIMD registers (F0-F31)
	// Use FLDPD to restore pairs of 64-bit floating-point registers
	FLDPD 480(RSP), (F30, F31)
	FLDPD 464(RSP), (F28, F29)
	FLDPD 448(RSP), (F26, F27)
	FLDPD 432(RSP), (F24, F25)
	FLDPD 416(RSP), (F22, F23)
	FLDPD 400(RSP), (F20, F21)
	FLDPD 384(RSP), (F18, F19)
	FLDPD 368(RSP), (F16, F17)
	FLDPD 352(RSP), (F14, F15)
	FLDPD 336(RSP), (F12, F13)
	FLDPD 320(RSP), (F10, F11)
	FLDPD 304(RSP), (F8, F9)
	FLDPD 288(RSP), (F6, F7)
	FLDPD 272(RSP), (F4, F5)
	FLDPD 256(RSP), (F2, F3)
	FLDPD 240(RSP), (F0, F1)

	// Restore floating-point status
	MOVD 232(RSP), R0
	MSR R0, FPSR

	// Restore condition flags
	MOVD 224(RSP), R0
	MSR R0, NZCV

	// Restore general-purpose registers (R0-R26, then R27 separately)
	LDP 200(RSP), (R25, R26)
	LDP 184(RSP), (R23, R24)
	LDP 168(RSP), (R21, R22)
	LDP 152(RSP), (R19, R20)
	LDP 136(RSP), (R16, R17)
	LDP 120(RSP), (R14, R15)
	LDP 104(RSP), (R12, R13)
	LDP 88(RSP), (R10, R11)
	LDP 72(RSP), (R8, R9)
	LDP 56(RSP), (R6, R7)
	LDP 40(RSP), (R4, R5)
	LDP 24(RSP), (R2, R3)
	LDP 8(RSP), (R0, R1)

	// Restore frame pointer
	MOVD 496(RSP), R29

	// Restore R27 (needed because we interrupt at arbitrary points)
	MOVD 216(RSP), R27

	// CRITICAL: Load saved LR (interrupted PC) into R30
	// We do this BEFORE deallocating the frame
	MOVD 0(RSP), R30           // R30 = interrupted PC

	// Deallocate our frame (504 bytes) + the 16 bytes pushCall allocated
	ADD $520, RSP

	// DEBUG: Print 'R' to show we're returning from asyncPreemptBM
	// Save R30 (interrupted PC) and R0 since UART_PUTC_SAFE uses R0 as parameter
	SUB $16, RSP
	STP (R0, R30), 0(RSP)
	MOVD $'R', R0
	UART_PUTC_SAFE
	LDP 0(RSP), (R0, R30)
	ADD $16, RSP

	// Jump to interrupted PC via ret (which jumps to R30/LR)
	// This restores execution at the interrupted location
	RET                        // Jump to LR (interrupted PC)
