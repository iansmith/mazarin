// preempt_riscv64.s - Timer IRQ handler for RISC-V thread preemption
//
// Pure-assembly timer IRQ handler that implements thread-level preemption.
// When a thread has run for too long (ThreadPreemptTicks exceeded),
// NeedsThreadPreempt is set so the exception handler can context-switch.
//
// Goroutine-level preemption is handled by the Go runtime in userspace via
// SIGURG signals — the kernel no longer injects asyncPreempt or poisons
// g.stackguard0.
//
// The handler:
//   1. Re-arms the timer via SBI set_timer using TimerRearmTicks (TickIntervalMs)
//   2. Validates preemption offsets and gets current thread from perCPU
//   3. Uses rdtime hardware counter for deadline comparisons
//   4. Initializes thread deadline fields on first tick (StartTick == 0)
//   5. Checks thread preemption deadline; sets NeedsThreadPreempt if exceeded
//
// Input: X2 = trap frame base (saved g at offset 208, sstatus at 256)
// Clobbers: T0, T1, A0-A5, A7 (saved/restored: A0-A5)

#include "textflag.h"

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Called from handle_timer_interrupt in exceptions_riscv64.s.
// X2 points to the trap frame on entry.
//
// func TimerIRQHandlerAsm()
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
	// Save registers we'll use (A0-A5 = 48 bytes)
	ADD	$-48, X2
	MOV	A0, 0(X2)
	MOV	A1, 8(X2)
	MOV	A2, 16(X2)
	MOV	A3, 24(X2)
	MOV	A4, 32(X2)
	MOV	A5, 40(X2)

	// ========================================================================
	// Step 1: Re-arm timer immediately
	// ========================================================================
	// Read hardware time counter
	WORD	$0xC0102573		// rdtime a0
	MOV	A0, A3			// A3 = current time (preserved for deadlines)

	// Re-arm: deadline = current + TimerRearmTicks
	MOV	·TimerRearmTicks(SB), A1
	ADD	A3, A1, A0		// A0 = new deadline
	MOV	$0, A7			// SBI legacy set_timer (extension 0)
	WORD	$0x00000073		// ECALL

	// Re-enable timer interrupt (STIE = bit 5 in SIE register)
	MOV	$0x20, A0
	WORD	$0x10452073		// CSRS sie, a0

	// Increment global timer IRQ counter
	MOV	·TimerIRQCount(SB), A0
	ADD	$1, A0, A0
	MOV	A0, ·TimerIRQCount(SB)

	// ========================================================================
	// Step 2: Validate preemption offsets and get current thread
	// ========================================================================
	MOVW	·PreemptOffsetsValid(SB), A0
	BEQ	A0, ZERO, rv_timer_done

	// Per-CPU pointer: &perCPUData + hartID * PerCPUSize
	MOV	TP, A1			// A1 = hart ID
	MOV	main·PerCPUSize(SB), A2
	MUL	A1, A2, A1		// A1 = offset
	MOV	$main·perCPUData(SB), A2
	ADD	A1, A2, A2		// A2 = perCPU pointer (preserved)

	// Increment local tick counter (perCPU offset 24)
	MOV	24(A2), A0
	ADD	$1, A0, A0
	MOV	A0, 24(A2)

	// Load current thread (perCPU offset 0)
	MOV	0(A2), A4		// A4 = *Thread (preserved)
	BEQ	A4, ZERO, rv_timer_done

	// ========================================================================
	// Step 3: Check if thread deadline is initialized
	// ========================================================================
	MOV	main·ThreadStartTickOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5		// A5 = thread.StartTick
	BEQ	A5, ZERO, rv_init_deadlines

	// ========================================================================
	// Step 4: Thread deadline check
	// ========================================================================
	MOV	main·ThreadPreemptDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5		// A5 = ThreadPreemptDeadline
	BLT	A3, A5, rv_timer_done

	// Thread deadline exceeded
	MOV	$1, A0
	MOVW	A0, 20(A2)		// perCPU.NeedsThreadPreempt (offset 20)

rv_timer_done:
	// Restore saved registers
	MOV	0(X2), A0
	MOV	8(X2), A1
	MOV	16(X2), A2
	MOV	24(X2), A3
	MOV	32(X2), A4
	MOV	40(X2), A5
	ADD	$48, X2

	RET

rv_init_deadlines:
	// ========================================================================
	// First tick for this thread — initialize thread deadline
	// ========================================================================
	MOV	main·ThreadStartTickOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A3, (A5)		// thread.StartTick = current tick

	MOV	·ThreadPreemptTicks(SB), A1
	ADD	A3, A1, A1
	MOV	main·ThreadPreemptDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A1, (A5)		// ThreadPreemptDeadline = current + threshold

	JMP	rv_timer_done
