
//go:build !test_stubs

#include "textflag.h"

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Called from exceptions_arm64.s when IRQ 27 (timer) is detected.
//
// This handler does NOT call any Go functions, making it safe to execute
// from IRQ context where the Go runtime may be in an inconsistent state.
//
// The handler:
//   1. Re-arms the timer for the next tick (~10ms)
//   2. Validates that preemption offsets are initialized
//   3. Gets current thread and checks thread preemption deadline
//   4. Sets NeedsThreadPreempt if a thread has exceeded its time quantum
//
// Goroutine-level preemption is handled by the Go runtime in userspace via
// SIGURG signals — the kernel no longer injects asyncPreempt or poisons
// g.stackguard0.
//
// Clobbers: R0-R9 (caller must save if needed)
//
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
	// ========================================================================
	// Step 1: Re-arm timer immediately
	// ========================================================================
	// This ensures we get the next tick even if we abort early.
	// Use relative timer (CNTV_TVAL_EL0) for simplicity.
	// 10ms at 62.5MHz = 625000 ticks (default, actual computed at runtime)

	// Load tick count from SystemTimerFrequency
	// ticks = (freq * 10) / 1000 = freq / 100
	MOVD	·SystemTimerFrequency(SB), R0
	MOVD	$100, R1
	UDIV	R1, R0, R0  // R0 = freq / 100 = ticks for 10ms

	// If frequency not set, use default
	CBZ	R0, use_default_ticks
	B	rearm_timer

use_default_ticks:
	MOVD	$625000, R0  // Default: 62.5MHz / 100 = 625000

rearm_timer:
	// Read current counter: MRS X1, CNTVCT_EL0
	WORD	$0xD53BE041

	// Calculate new compare value
	ADD	R0, R1, R1

	// Write to CNTV_CVAL_EL0: MSR CNTV_CVAL_EL0, X1
	WORD	$0xD51BE341

	// Ensure timer enabled: MSR CNTV_CTL_EL0, X2 (with value 1)
	MOVD	$1, R2
	WORD	$0xD51BE322

	// Increment timer IRQ counter
	MOVD	·TimerIRQCount(SB), R0
	ADD	$1, R0
	MOVD	R0, ·TimerIRQCount(SB)
	DSB	$15  // DSB SY - ensure store completes

	// ========================================================================
	// Step 2: Check if preemption offsets are initialized
	// ========================================================================
	MOVW	·PreemptOffsetsValid(SB), R0  // uint32 - use MOVW not MOVD
	CBNZ	R0, offsets_valid
	B	timer_return  // Offsets not valid yet
offsets_valid:

	// ========================================================================
	// Step 3: Get current thread pointer
	// ========================================================================
	MOVD	main·CurrentThread(SB), R7  // *Thread
	CBZ	R7, timer_return  // currentThread is nil

	// ========================================================================
	// Step 4: Read current counter and check thread deadline
	// ========================================================================
	// MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

	// Check if StartTick is initialized
	MOVD	main·ThreadStartTickOffset(SB), R5
	ADD	R7, R5
	MOVD	(R5), R8  // R8 = currentThread.StartTick
	CBZ	R8, init_deadlines  // Not initialized yet

	// Check thread deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	main·ThreadPreemptDeadlineOffset(SB), R5
	ADD	R7, R5
	MOVD	(R5), R8  // R8 = ThreadPreemptDeadline

	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	timer_return  // if current < deadline (negative), no preemption

	// Current >= thread deadline: signal preemption
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

init_deadlines:
	// Deadlines not initialized - initialize them
	// R9 = current counter
	MOVD	main·ThreadStartTickOffset(SB), R5
	ADD	R7, R5
	MOVD	R9, (R5)  // currentThread.StartTick = current tick

	// Set thread deadline: current + threshold
	MOVD	·ThreadPreemptTicks(SB), R8
	ADD	R9, R8, R8
	MOVD	main·ThreadPreemptDeadlineOffset(SB), R5
	ADD	R7, R5
	MOVD	R8, (R5)  // ThreadPreemptDeadline = current + threshold
	B	timer_return

timer_return:
	RET
