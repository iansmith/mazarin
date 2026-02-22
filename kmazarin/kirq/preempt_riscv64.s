//go:build !test_stubs

// preempt_riscv64.s - Timer IRQ handler for RISC-V cooperative preemption
//
// Pure-assembly timer IRQ handler that implements cooperative preemption
// by poisoning g.stackguard0 when a goroutine exceeds its time quantum.
// Uses hardware rdtime counter for deadline-based preemption, matching
// the ARM64 pattern in preempt_arm64.s.
//
// The handler:
//   1. Re-arms the timer via SBI set_timer using TimerRearmTicks
//   2. Validates preemption offsets and gets current thread from perCPU
//   3. Uses rdtime hardware counter for deadline comparisons
//   4. Initializes deadline fields on first tick (StartTick == 0)
//   5. Detects goroutine changes via g pointer comparison
//   6. Checks goroutine preemption deadline; poisons g if exceeded
//   7. Checks thread preemption deadline
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
	// Read saved g from trap frame BEFORE allocating save area.
	// 208(X2) = pre-trap X27 (g register), saved by trapEntry.
	MOV	208(X2), T0		// T0 = saved g (preserved throughout)

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
	// Step 3: A3 = rdtime value from Step 1
	// ========================================================================
	// Validate saved g pointer
	BEQ	T0, ZERO, rv_timer_done

	// ========================================================================
	// Step 4: Initialize deadlines if thread.StartTick == 0
	// ========================================================================
	MOV	main·ThreadStartTickOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5		// A5 = thread.StartTick
	BEQ	A5, ZERO, rv_init_deadlines

	// ========================================================================
	// Step 5: G-change detection
	// ========================================================================
	MOV	main·ThreadLastSeenGOffset(SB), A5
	ADD	A4, A5, A5		// A5 = &thread.LastSeenG
	MOV	(A5), A1		// A1 = thread.LastSeenG
	BEQ	T0, A1, rv_same_goroutine

	// G changed — update LastSeenG, reset goroutine deadline.
	// Don't reset ThreadPreemptDeadline (track total thread time).
	MOV	T0, (A5)		// thread.LastSeenG = current g

	MOV	main·ThreadGoroutineStartOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A3, (A5)		// thread.GoroutineStart = current tick

	MOV	·GoroutinePreemptTicks(SB), A1
	ADD	A3, A1, A1		// new deadline = current + threshold
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A1, (A5)		// thread.GoroutinePreemptDeadline = new deadline

	JMP	rv_check_thread_deadline

rv_same_goroutine:
	// ========================================================================
	// Step 6: Goroutine deadline check
	// ========================================================================
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5		// A5 = GoroutinePreemptDeadline
	BLT	A3, A5, rv_check_thread_deadline

	// Deadline exceeded — validate g status and poison if running.
	// Set SUM bit to safely access g struct. Needed for user-mode g
	// (U-bit pages). Harmless for kernel g (no U-bit on kernel pages).
	MOV	$0x40000, A0		// SUM = sstatus bit 18
	WORD	$0x10052073		// CSRS sstatus, a0

	// Read g.atomicstatus (32-bit)
	MOV	·PreemptGStatusOffset(SB), A1
	ADD	T0, A1, A1
	MOVW	(A1), A1		// A1 = g.atomicstatus

	// Mask off _Gscan bit and check for _Grunning
	MOVW	·PreemptGScan(SB), A0
	XOR	$-1, A0, A0		// NOT
	AND	A0, A1, A1		// status & ~_Gscan
	MOVW	·PreemptGRunning(SB), A0
	BNE	A1, A0, rv_clear_sum	// not _Grunning → skip poison

	// Poison g for cooperative preemption
	MOV	·PreemptPreemptOffset(SB), A1
	ADD	T0, A1, A1
	MOV	$1, A0
	MOV	A0, (A1)		// g.preempt = true

	MOV	·PreemptStackGuard0Offset(SB), A1
	ADD	T0, A1, A1
	MOV	·PreemptStackPreemptValue(SB), A0
	MOV	A0, (A1)		// g.stackguard0 = stackPreempt

	// Signal async preemption needed
	MOV	$1, A0
	MOVW	A0, 16(A2)		// perCPU.NeedsAsyncPreempt (offset 16)

	// Reset goroutine deadline
	MOV	·GoroutinePreemptTicks(SB), A1
	ADD	A3, A1, A1		// new deadline
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A0
	ADD	A4, A0, A0
	MOV	A1, (A0)

rv_clear_sum:
	// Clear SUM bit
	MOV	$0x40000, A0
	WORD	$0x10053073		// CSRC sstatus, a0

rv_check_thread_deadline:
	// ========================================================================
	// Step 7: Thread deadline check
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
	// First tick for this thread — initialize all deadline fields
	// ========================================================================
	MOV	main·ThreadStartTickOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A3, (A5)		// thread.StartTick = current tick

	MOV	main·ThreadGoroutineStartOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A3, (A5)		// thread.GoroutineStart = current tick

	MOV	main·ThreadLastSeenGOffset(SB), A5
	ADD	A4, A5, A5
	MOV	T0, (A5)		// thread.LastSeenG = current g

	MOV	·ThreadPreemptTicks(SB), A1
	ADD	A3, A1, A1
	MOV	main·ThreadPreemptDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A1, (A5)		// ThreadPreemptDeadline = current + threshold

	MOV	·GoroutinePreemptTicks(SB), A1
	ADD	A3, A1, A1
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	A1, (A5)		// GoroutinePreemptDeadline = current + threshold

	JMP	rv_timer_done
