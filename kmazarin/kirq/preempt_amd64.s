//go:build !test_stubs

// preempt_amd64.s - Timer IRQ handler for x86_64 thread preemption
//
// This is the pure-assembly timer IRQ handler that implements thread-level
// preemption. When a thread has run for too long (ThreadPreemptTicks exceeded),
// NeedsThreadPreempt is set so the exception handler can context-switch.
//
// Goroutine-level preemption is handled by the Go runtime in userspace via
// SIGURG signals — the kernel no longer injects asyncPreempt or poisons
// g.stackguard0.
//
// The handler:
// 1. Increments the local tick counter
// 2. Checks if the current thread has exceeded its time quantum
// 3. If so, sets NeedsThreadPreempt for the exception handler
// 4. Re-arms the LAPIC timer

#include "textflag.h"

#define LAPIC_VA	(0xFEE00000 + 0xFFFFFFFF00000000)
#define LAPIC_EOI	0xB0
#define LAPIC_LVT_TMR	0x320
#define LAPIC_TMRINITCNT	0x380
#define LAPIC_TMRDIV	0x3E0

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Does NOT call any Go functions. Sets NeedsThreadPreempt when
// a thread has exceeded its time quantum.
//
// func TimerIRQHandlerAsm()
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
	// Save registers we'll use
	PUSHQ	AX
	PUSHQ	BX
	PUSHQ	CX
	PUSHQ	DX
	PUSHQ	SI

	// Increment tick counter
	MOVQ	·TimerIRQCount(SB), AX
	INCQ	AX
	MOVQ	AX, ·TimerIRQCount(SB)
	// AX = current tick

	// Get per-CPU data pointer
	// On x86_64, we use CPUID to get APIC ID
	MOVQ	AX, SI			// Save tick in SI
	MOVL	$1, AX
	CPUID
	SHRL	$24, BX			// BX = APIC ID (CPU ID)

	// Compute perCPU pointer: &perCPUData + cpuID * PerCPUSize
	MOVQ	main·PerCPUSize(SB), CX
	MOVBQZX	BL, AX
	IMULQ	CX, AX			// AX = offset
	LEAQ	main·perCPUData(SB), CX
	ADDQ	CX, AX			// AX = perCPU pointer

	// Increment local tick counter (offset 24 in PerCPU)
	MOVQ	24(AX), CX
	INCQ	CX
	MOVQ	CX, 24(AX)

	// Load current thread pointer (offset 0 in PerCPU)
	MOVQ	0(AX), BX		// BX = *Thread (or nil)
	TESTQ	BX, BX
	JZ	timer_done		// No current thread

	// Check thread preemption deadline
	// Check thread preemption deadline
	MOVQ	main·ThreadPreemptDeadlineOffset(SB), DX
	ADDQ	BX, DX
	MOVQ	(DX), DX		// DX = thread deadline
	MOVQ	24(AX), CX		// CX = current tick
	CMPQ	CX, DX
	JL	timer_done

	// Set NeedsThreadPreempt flag in perCPU (offset 20, uint32)
	MOVL	$1, 20(AX)

timer_done:
	// Re-arm LAPIC timer
	MOVQ	$LAPIC_VA, CX

	// Set divide by 16
	MOVL	$3, LAPIC_TMRDIV(CX)

	// Set LVT timer: one-shot, vector 0x30, not masked
	MOVL	$0x30, LAPIC_LVT_TMR(CX)

	// Set initial count: TickIntervalMs (4ms) at ~62.5MHz effective rate = 250000
	// NOTE: This handler is not called on x86_64 (Go path handles timer re-arm
	// via TimerIRQHandlerCanPreempt → ktimer.Rearm(TimerRearmTicks)).
	// Kept for reference / potential future use.
	MOVL	$250000, LAPIC_TMRINITCNT(CX)

	// Send EOI
	MOVL	$0, LAPIC_EOI(CX)

	// Restore registers
	POPQ	SI
	POPQ	DX
	POPQ	CX
	POPQ	BX
	POPQ	AX
	RET
