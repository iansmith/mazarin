#include "textflag.h"

// async_preempt_arm64.s - Async Preemption Handler
//
// ============================================================================
// OVERVIEW
// ============================================================================
// Handles asynchronous preemption injected by timer interrupts. Based on
// Go runtime's asyncPreempt mechanism but adapted for bare-metal.
//
// Functions:
//   - asyncPreemptBM() - Preemption handler (9 segments, 504-byte frame)
//
// OPERATION:
// Timer interrupt handler modifies ELR_EL1 to inject execution here:
//   1. Entry: LR = interrupted PC, SP = interrupted SP - 16
//   2. Allocates 504-byte frame
//   3. Saves ALL registers (R0-R29, FP/SIMD F0-F31, status flags NZCV/FPSR)
//   4. Calls timerPreempt() which calls runtime.Gosched() to yield CPU
//   5. When rescheduled, restores ALL registers
//   6. Returns to interrupted PC (via LR)
//
// WHY NOT DECOMPOSE:
// Async preemption is a critical orchestration that cannot be decomposed:
//   1. ALL registers must be saved atomically - no function calls allowed
//   2. Frame layout must match what timer interrupt handler expects
//   3. Must preserve exact execution state for transparent preemption
//   4. Cannot use any primitives that might clobber registers
// Decomposing would corrupt state and break preemptive scheduling.
//
// ============================================================================
// SEGMENT OVERVIEW (see asm/docs/small_files_decomposition.md)
// ============================================================================
//
// asyncPreemptBM segments:
//   Segment 1: Allocate Frame & Save LR - Save interrupted PC, set up frame
//   Segment 2: Save GPRs - Save R0-R27 to frame
//   Segment 3: Save Status Regs - Save NZCV, FPSR condition flags
//   Segment 4: Save FP/SIMD - Save F0-F31 floating-point registers
//   Segment 5: Call Go Handler - Call timerPreempt() -> Gosched()
//   Segment 6: Restore FP/SIMD - Restore F0-F31
//   Segment 7: Restore Status Regs - Restore NZCV, FPSR
//   Segment 8: Restore GPRs - Restore R0-R27, R29
//   Segment 9: Return - Load LR, deallocate frame, RET to interrupted PC
//
// POST-BOOT NOTE: This is preemption code - must remain inline assembly.
// Timer interrupts inject execution here; all registers must be preserved.
//
// This function is "injected" by the timer interrupt handler.
// The interrupt handler modifies ELR_EL1 to point here, with LR set to interrupted PC.
// This saves all registers, calls the scheduler, then returns to interrupted location.

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
	// ========================================================================
	// SEGMENT 1: Allocate Frame & Save LR
	// ========================================================================
	// Save LR (= interrupted PC) before allocating frame.
	SUB $504, RSP
	MOVD R30, 0(RSP)           // Store interrupted PC at [SP+0]
	MOVD R29, 496(RSP)         // Save FP
	ADD $496, RSP, R29         // FP = SP + 496

	// ========================================================================
	// SEGMENT 2: Save GPRs
	// ========================================================================
	// Save R0-R27 (skip g, R29=FP already saved, R30=LR already saved).
	// NOTE: R18 (platform register) is intentionally omitted - Go Plan9 assembly
	// doesn't support it directly. Cardinal's bare-metal context doesn't use R18,
	// unlike kmazarin which saves it via raw WORD instructions. This is consistent
	// with all of cardinal's exception handlers (see exc_sync_entry_arm64.s, etc).
	STP (R0, R1), 8(RSP)
	STP (R2, R3), 24(RSP)      // Save R2, R3
	STP (R4, R5), 40(RSP)      // Save R4, R5
	STP (R6, R7), 56(RSP)      // Save R6, R7
	STP (R8, R9), 72(RSP)      // Save R8, R9
	STP (R10, R11), 88(RSP)    // Save R10, R11
	STP (R12, R13), 104(RSP)   // Save R12, R13
	STP (R14, R15), 120(RSP)   // Save R14, R15
	STP (R16, R17), 136(RSP)   // Save R16, R17
	// R18 skipped (see NOTE above)
	STP (R19, R20), 152(RSP)   // Save R19, R20
	STP (R21, R22), 168(RSP)   // Save R21, R22
	STP (R23, R24), 184(RSP)   // Save R23, R24
	STP (R25, R26), 200(RSP)   // Save R25, R26
	MOVD R27, 216(RSP)         // Save R27 (CRITICAL: interrupt at arbitrary points!)

	// ========================================================================
	// SEGMENT 3: Save Status Regs
	// ========================================================================
	// Save NZCV and FPSR condition flags.
	MRS NZCV, R0
	MOVD R0, 224(RSP)
	MRS FPSR, R0
	MOVD R0, 232(RSP)

	// ========================================================================
	// SEGMENT 4: Save FP/SIMD
	// ========================================================================
	// Save F0-F31 (64-bit view, matching Go runtime's asyncPreempt).
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

	// ========================================================================
	// SEGMENT 5: Call Go Handler
	// ========================================================================
	// Call timerPreempt() which calls runtime.Gosched().
	CALL main·timerPreempt(SB)

	// ========================================================================
	// SEGMENT 6: Restore FP/SIMD
	// ========================================================================
	// Restore F0-F31.
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

	// ========================================================================
	// SEGMENT 7: Restore Status Regs
	// ========================================================================
	// Restore NZCV and FPSR.
	MOVD 232(RSP), R0
	MSR R0, FPSR
	MOVD 224(RSP), R0
	MSR R0, NZCV

	// ========================================================================
	// SEGMENT 8: Restore GPRs
	// ========================================================================
	// Restore R0-R27, R29.
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

	MOVD 216(RSP), R27         // Restore R27

	// ========================================================================
	// SEGMENT 9: Return
	// ========================================================================
	// Load LR (interrupted PC), deallocate frame, return.
	MOVD 0(RSP), R30           // R30 = interrupted PC
	ADD $520, RSP              // Deallocate 504 + 16 bytes
	RET                        // Jump to LR (interrupted PC)

// get_asyncPreemptBM_addr returns the address of asyncPreemptBM
// This is used by Go code to get the function address for timer preemption
// Returns: R0 = address of asyncPreemptBM
TEXT get_asyncPreemptBM_addr(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $asyncPreemptBM(SB), R0
	MOVD R0, ret+0(FP)
	RET
