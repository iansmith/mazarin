#include "textflag.h"

// LAPIC registers (physical addresses, add KernelMMIOOffset for VA)
#define LAPIC_PHYS_BASE   0xFEE00000
#define LAPIC_LVT_TMR     0x320
#define LAPIC_TMRINITCNT   0x380
#define LAPIC_TMRDIV       0x3E0
#define KERNEL_MMIO_OFFSET 0xFFFFFFFF00000000

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

// RearmTimerNow re-arms the LAPIC timer to fire after ~10ms.
// Uses one-shot mode with divide-by-1 (matching PlatformTimerInit calibration).
// func RearmTimerNow()
TEXT ·RearmTimerNow(SB), NOSPLIT, $0-0
	// LAPIC VA = LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET
	MOVQ	$(LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET), CX

	// Set timer divide: divide by 1 (value 0x0B) — must match PlatformTimerInit
	MOVL	$0x0B, AX
	MOVL	AX, LAPIC_TMRDIV(CX)

	// Set LVT timer: one-shot, vector 48 (0x30), not masked
	MOVL	$0x30, AX			// vector 48, one-shot mode
	MOVL	AX, LAPIC_LVT_TMR(CX)

	// Set initial count for ~10ms at ~1GHz bus clock (divide-by-1)
	// QEMU LAPIC timer runs at bus frequency (~1GHz typically)
	// 10ms * 1GHz = 10,000,000 ticks
	MOVL	$10000000, AX
	MOVL	AX, LAPIC_TMRINITCNT(CX)

	RET

// DisableTimerHardware disables the LAPIC timer by masking the LVT entry.
// func DisableTimerHardware()
TEXT ·DisableTimerHardware(SB), NOSPLIT, $0-0
	MOVQ	$(LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET), CX
	MOVL	$(1<<16), AX			// LVT_MASKED
	MOVL	AX, LAPIC_LVT_TMR(CX)

	// Zero the initial count to stop the timer
	MOVL	$0, AX
	MOVL	AX, LAPIC_TMRINITCNT(CX)

	RET
