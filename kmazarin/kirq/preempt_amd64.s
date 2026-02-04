//go:build !test_stubs

// preempt_amd64.s - Timer IRQ handler for x86_64 cooperative preemption
//
// This is the pure-assembly timer IRQ handler that implements cooperative
// preemption by poisoning g.stackguard0 when a goroutine has exceeded
// its time quantum. This avoids calling any Go functions from the IRQ
// context, preventing "morestack on g0" crashes.
//
// The handler:
// 1. Increments the local tick counter
// 2. Checks if the current thread's goroutine should be preempted
// 3. If so, writes stackPreempt to g.stackguard0 (cooperative preemption)
// 4. Re-arms the LAPIC timer

#include "textflag.h"

#define LAPIC_VA	(0xFEE00000 + 0xFFFFFFFF00000000)
#define LAPIC_EOI	0xB0
#define LAPIC_LVT_TMR	0x320
#define LAPIC_TMRINITCNT	0x380
#define LAPIC_TMRDIV	0x3E0

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Does NOT call any Go functions. Sets g.preempt and g.stackguard0
// directly for cooperative preemption.
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

	// Check goroutine preemption deadline
	// Load goroutine deadline offset
	MOVQ	main·ThreadGoroutineDeadlineOffset(SB), DX
	ADDQ	BX, DX			// DX = &thread.GoroutinePreemptDeadline
	MOVQ	(DX), DX		// DX = deadline value
	CMPQ	CX, DX			// current tick vs deadline
	JL	check_thread_deadline	// Not yet past deadline

	// Goroutine deadline exceeded - set cooperative preemption
	// Get g pointer from R14 (but we can't read R14 here since it's saved)
	// Instead, read g from the thread's lastSeenG field
	MOVQ	main·ThreadLastSeenGOffset(SB), DX
	ADDQ	BX, DX
	MOVQ	(DX), DX		// DX = g pointer
	TESTQ	DX, DX
	JZ	check_thread_deadline

	// Read g.atomicstatus to check if goroutine is running
	MOVQ	·PreemptGStatusOffset(SB), CX
	ADDQ	DX, CX			// CX = &g.atomicstatus
	MOVL	(CX), CX		// CX = atomicstatus (32-bit)
	// Mask off _Gscan bit
	ANDL	$0xFFFFEFFF, CX		// Clear bit 12 (_Gscan)
	CMPL	CX, $2			// _Grunning = 2
	JNE	check_thread_deadline

	// Set g.preempt = true
	MOVQ	·PreemptPreemptOffset(SB), CX
	ADDQ	DX, CX
	MOVQ	$1, (CX)

	// Set g.stackguard0 = stackPreempt
	MOVQ	·PreemptStackGuard0Offset(SB), CX
	ADDQ	DX, CX
	MOVQ	·PreemptStackPreemptValue(SB), SI
	MOVQ	SI, (CX)

	// Reset goroutine deadline
	MOVQ	·GoroutinePreemptTicks(SB), SI
	MOVQ	24(AX), CX		// current tick from perCPU
	ADDQ	SI, CX			// new deadline
	MOVQ	main·ThreadGoroutineDeadlineOffset(SB), SI
	ADDQ	BX, SI
	MOVQ	CX, (SI)

check_thread_deadline:
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

	// Set initial count (~10ms at ~62.5MHz effective rate)
	MOVL	$625000, LAPIC_TMRINITCNT(CX)

	// Send EOI
	MOVL	$0, LAPIC_EOI(CX)

	// Restore registers
	POPQ	SI
	POPQ	DX
	POPQ	CX
	POPQ	BX
	POPQ	AX
	RET
