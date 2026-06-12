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
#define LAPIC_INITIAL_COUNT 0x380
#define ONESHOT_VEC30 0x00000030

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

	// Get per-CPU data pointer.
	// MAZ-136: read the cached APIC ID (main.cpuIDCache) instead of executing
	// CPUID here. CPUID is an unconditional VM-exit; running it every timer tick
	// fed the nested-KVM x86 exit storm. The cache is filled once at boot
	// (initCPUIDCacheArch); 0 before then, which is the correct BSP id.
	MOVQ	AX, SI			// Save tick in SI
	MOVQ	main·cpuIDCache(SB), BX	// BX = cached APIC ID (CPU ID)

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
	MOVQ	main·ThreadPreemptDeadlineOffset(SB), DX
	ADDQ	BX, DX
	MOVQ	(DX), DX		// DX = thread deadline
	MOVQ	24(AX), CX		// CX = current tick
	CMPQ	CX, DX
	JL	timer_done

	// Set NeedsThreadPreempt flag in perCPU (offset 20, uint32)
	MOVL	$1, 20(AX)

timer_done:
	// Re-arm LAPIC timer via one-shot mode
	MOVQ	$LAPIC_VA, CX

	// Set LVT timer: one-shot mode, vector 0x30
	MOVL	$ONESHOT_VEC30, LAPIC_LVT_TMR(CX)

	// Write initial count (~4M LAPIC ticks)
	MOVL	$4000000, LAPIC_INITIAL_COUNT(CX)

	// Send EOI
	MOVL	$0, LAPIC_EOI(CX)

	// Restore registers
	POPQ	SI
	POPQ	DX
	POPQ	CX
	POPQ	BX
	POPQ	AX
	RET
