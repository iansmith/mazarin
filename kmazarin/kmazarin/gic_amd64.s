#include "textflag.h"

// LAPIC registers (physical addresses, add KernelVAOffset for VA)
#define LAPIC_PHYS_BASE   0xFEE00000
#define LAPIC_LVT_TMR     0x320
#define LAPIC_INITIAL_COUNT 0x380
#define KERNEL_MMIO_OFFSET 0xFFFFFFFF00000000

// LVT Timer: One-shot mode (bits 18:17 = 00), vector 0x30, unmasked
#define ONESHOT_VEC30 0x00000030

// ReadTSC reads the Time Stamp Counter.
// func ReadTSC() uint64
TEXT ·ReadTSC(SB), NOSPLIT, $0-8
	RDTSC				// EAX=low32, EDX=high32
	SHLQ	$32, DX			// Shift high bits
	ORQ	DX, AX			// Combine into 64-bit
	MOVQ	AX, ret+0(FP)
	RET

// ReadRFLAGS reads the RFLAGS register.
// func ReadRFLAGS() uint64
TEXT ·ReadRFLAGS(SB), NOSPLIT, $0-8
	PUSHFQ
	POPQ	AX
	MOVQ	AX, ret+0(FP)
	RET

// RearmTimerNow re-arms the LAPIC timer to fire after ~10ms using one-shot mode.
// Uses a fixed initial count based on typical LAPIC timer frequency (~200KHz under TCG).
// func RearmTimerNow()
TEXT ·RearmTimerNow(SB), NOSPLIT, $0-0
	// Set LVT Timer to one-shot mode, vector 0x30
	MOVQ	$(LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET), CX
	MOVL	$ONESHOT_VEC30, AX
	MOVL	AX, LAPIC_LVT_TMR(CX)

	// Write initial count — use a large value for safety.
	// The actual tick interval is calibrated by PlatformTimerInit.
	// 10,000,000 LAPIC ticks ≈ 10ms at 1GHz LAPIC frequency,
	// or 50s at 200KHz (QEMU TCG). Either way, the interrupt fires.
	MOVL	$10000000, AX
	MOVL	AX, LAPIC_INITIAL_COUNT(CX)

	RET

// DisableTimerHardware disables the LAPIC timer by masking the LVT entry.
// func DisableTimerHardware()
TEXT ·DisableTimerHardware(SB), NOSPLIT, $0-0
	MOVQ	$(LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET), CX
	MOVL	$(1<<16), AX			// LVT_MASKED
	MOVL	AX, LAPIC_LVT_TMR(CX)

	// Zero the initial count to stop the timer
	MOVL	$0, LAPIC_INITIAL_COUNT(CX)

	RET
