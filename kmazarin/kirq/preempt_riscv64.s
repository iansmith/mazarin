//go:build !test_stubs

// preempt_riscv64.s - Timer IRQ handler for RISC-V cooperative preemption
//
// Pure-assembly timer IRQ handler that implements cooperative preemption
// by poisoning g.stackguard0 when a goroutine exceeds its time quantum.
// Does NOT call any Go functions.
//
// The handler:
// 1. Increments the local tick counter
// 2. Checks if the current thread's goroutine should be preempted
// 3. If so, writes stackPreempt to g.stackguard0
// 4. Re-arms the timer via SBI set_timer

#include "textflag.h"

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Does NOT call any Go functions.
//
// func TimerIRQHandlerAsm()
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
	// Save registers we'll use
	// We're on the exception stack, push temporaries
	ADD	$-48, X2
	MOV	A0, 0(X2)
	MOV	A1, 8(X2)
	MOV	A2, 16(X2)
	MOV	A3, 24(X2)
	MOV	A4, 32(X2)
	MOV	A5, 40(X2)

	// Increment global tick counter
	MOV	·TimerIRQCount(SB), A0
	ADD	$1, A0, A0
	MOV	A0, ·TimerIRQCount(SB)

	// Get per-CPU data pointer using hart ID from tp
	MOV	TP, A1				// hart ID
	MOV	main·PerCPUSize(SB), A2
	MUL	A1, A2, A1			// offset = hartID * PerCPUSize
	MOV	$main·perCPUData(SB), A2
	ADD	A1, A2, A2			// A2 = perCPU pointer

	// Increment local tick counter (offset 24)
	MOV	24(A2), A3
	ADD	$1, A3, A3
	MOV	A3, 24(A2)

	// Load current thread pointer (offset 0)
	MOV	0(A2), A4			// A4 = *Thread
	BEQ	A4, ZERO, timer_done

	// Check goroutine preemption deadline
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A5
	ADD	A4, A5, A5			// &thread.GoroutineDeadline
	MOV	(A5), A5			// deadline
	BLT	A3, A5, check_thread_deadline	// tick < deadline

	// Goroutine deadline exceeded
	// Read g from thread.lastSeenG
	MOV	main·ThreadLastSeenGOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5			// A5 = g pointer
	BEQ	A5, ZERO, check_thread_deadline

	// Read g.atomicstatus
	MOV	·PreemptGStatusOffset(SB), A1
	ADD	A5, A1, A1
	MOVW	(A1), A1			// atomicstatus (32-bit)
	// Mask off _Gscan bit (0x1000)
	MOV	·PreemptGScan(SB), A0
	// BIC equivalent: A1 = A1 & ~A0
	// RISC-V doesn't have BIC, use ANDN or manual
	XOR	$-1, A0, A0			// NOT A0
	AND	A0, A1, A1
	// Check if _Grunning (2)
	MOV	·PreemptGRunning(SB), A0
	BNE	A1, A0, check_thread_deadline

	// Set g.preempt = true
	MOV	·PreemptPreemptOffset(SB), A1
	ADD	A5, A1, A1
	MOV	$1, A0
	MOV	A0, (A1)

	// Set g.stackguard0 = stackPreempt
	MOV	·PreemptStackGuard0Offset(SB), A1
	ADD	A5, A1, A1
	MOV	·PreemptStackPreemptValue(SB), A0
	MOV	A0, (A1)

	// Reset goroutine deadline
	MOV	·GoroutinePreemptTicks(SB), A1
	ADD	A3, A1, A1			// new deadline = tick + threshold
	MOV	main·ThreadGoroutineDeadlineOffset(SB), A0
	ADD	A4, A0, A0
	MOV	A1, (A0)

check_thread_deadline:
	// Check thread preemption deadline
	MOV	main·ThreadPreemptDeadlineOffset(SB), A5
	ADD	A4, A5, A5
	MOV	(A5), A5			// thread deadline
	MOV	24(A2), A3			// current tick
	BLT	A3, A5, timer_done

	// Set NeedsThreadPreempt (offset 20 in perCPU, uint32)
	MOV	$1, A0
	MOVW	A0, 20(A2)

timer_done:
	// Re-arm timer via SBI set_timer
	// Read current time
	// CSRR a0, time
	WORD	$0xC0102573			// rdtime a0
	// Add ~10ms (100000 ticks at 10MHz)
	MOV	$100000, A1
	ADD	A0, A1, A0
	// SBI set_timer: a0=stime_value, a7=0
	MOV	$0, A7
	WORD	$0x00000073			// ECALL

	// Re-enable timer interrupt in sie
	MOV	$0x20, A0			// STIE bit
	// CSRS sie, a0
	WORD	$0x10452073

	// Restore saved registers
	MOV	0(X2), A0
	MOV	8(X2), A1
	MOV	16(X2), A2
	MOV	24(X2), A3
	MOV	32(X2), A4
	MOV	40(X2), A5
	ADD	$48, X2

	RET
