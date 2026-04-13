//go:build amd64

#include "textflag.h"

// LAPIC constants (virtual addresses via kernel MMIO offset)
#define LAPIC_PHYS_BASE     0xFEE00000
#define KERNEL_MMIO_OFFSET  0xFFFFFFFF00000000
#define LAPIC_BASE          (LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET)
#define LAPIC_LVT_TIMER     0x320
#define LAPIC_INITIAL_COUNT 0x380
#define LAPIC_CURRENT_COUNT 0x390
#define LAPIC_DIVIDE_CONFIG 0x3E0

// LVT Timer: One-shot mode (bits 18:17 = 00), unmasked (bit 16 = 0), vector 0x30
#define ONESHOT_VEC30       0x00000030

// PlatformDisableTimer masks the LAPIC timer to stop generating interrupts.
// func PlatformDisableTimer()
TEXT ·PlatformDisableTimer(SB), NOSPLIT, $0-0
	MOVQ	$LAPIC_BASE, AX
	MOVL	LAPIC_LVT_TIMER(AX), BX
	ORL	$0x10000, BX		// Set bit 16 (masked)
	MOVL	BX, LAPIC_LVT_TIMER(AX)
	// Zero the initial count to stop the timer
	MOVL	$0, LAPIC_INITIAL_COUNT(AX)
	RET

// PlatformRearmTimer schedules the next timer interrupt after 'ticks' TSC ticks.
// Converts TSC ticks to LAPIC ticks using the PIT-calibrated ratio:
//   lapic_ticks = tsc_ticks * lapicElapsed / tscElapsed
// Then writes to the LAPIC initial count register (one-shot mode).
//
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOVQ	ticks+0(FP), R8		// R8 = TSC ticks

	// Convert TSC ticks → LAPIC ticks: R8 * lapicElapsed / tscElapsed
	LEAQ	·lapicElapsed(SB), AX
	MOVQ	(AX), R9		// R9 = lapicElapsed
	LEAQ	·tscElapsed(SB), AX
	MOVQ	(AX), R10		// R10 = tscElapsed

	// Guard: if tscElapsed is 0, use ticks directly as fallback
	TESTQ	R10, R10
	JZ	rearm_fallback

	// R8 = R8 * R9 / R10
	MOVQ	R8, AX
	MULQ	R9			// RDX:RAX = R8 * lapicElapsed
	DIVQ	R10			// RAX = (R8 * lapicElapsed) / tscElapsed
	MOVQ	AX, R8			// R8 = LAPIC ticks

rearm_fallback:
	// Clamp to 32-bit (LAPIC initial count is 32-bit)
	MOVQ	$0xFFFFFFFF, AX
	CMPQ	R8, AX
	CMOVQHI	AX, R8			// if R8 > 0xFFFFFFFF, R8 = 0xFFFFFFFF

	// Configure LVT Timer: one-shot mode, vector 0x30, unmasked
	MOVQ	$LAPIC_BASE, AX
	MOVL	$ONESHOT_VEC30, BX
	MOVL	BX, LAPIC_LVT_TIMER(AX)

	// Write initial count — timer starts counting down immediately
	MOVL	R8, LAPIC_INITIAL_COUNT(AX)

	RET

// PlatformTimerInit calibrates BOTH the TSC and LAPIC timer against PIT Channel 2
// in a single ~10ms measurement window. Returns the TSC frequency in Hz.
// Stores lapicElapsed and tscElapsed for PlatformRearmTimer's TSC→LAPIC conversion.
//
// func PlatformTimerInit() uint32
TEXT ·PlatformTimerInit(SB), NOSPLIT, $0-4
	// --- Configure LAPIC timer for calibration ---
	// Set divide config to 1 (divide by 1)
	// Divide value encoding: 0xB = divide by 1
	MOVQ	$LAPIC_BASE, R8
	MOVL	$0x0B, AX
	MOVL	AX, LAPIC_DIVIDE_CONFIG(R8)

	// Set LVT timer to one-shot, MASKED (no interrupt during calibration)
	MOVL	$(ONESHOT_VEC30 | 0x10000), AX	// vector 0x30, one-shot, masked
	MOVL	AX, LAPIC_LVT_TIMER(R8)

	// Set initial count to max (0xFFFFFFFF) — starts counting down
	MOVL	$0xFFFFFFFF, AX
	MOVL	AX, LAPIC_INITIAL_COUNT(R8)

	// --- PIT Channel 2 calibration (~10ms reference) ---
	// PIT oscillator: 1,193,182 Hz. Count 11932 ≈ 10.0013ms.

	// Disable PIT CH2 gate (port 0x61 bit 0)
	MOVW	$0x61, DX
	INB
	ANDB	$0xFC, AX		// Clear bits 0,1 (gate off, speaker off)
	OUTB

	// Program PIT CH2: mode 0 (terminal count), lobyte/hibyte, binary
	// Command byte: channel=2(10), access=lobyte/hibyte(11), mode=0(000), binary(0)
	// = 10_11_000_0 = 0xB0
	MOVW	$0x43, DX
	MOVB	$0xB0, AX
	OUTB

	// Write count = 11932 (0x2E9C)
	MOVW	$0x42, DX
	MOVB	$0x9C, AX		// Low byte
	OUTB
	MOVB	$0x2E, AX		// High byte
	OUTB

	// Read LAPIC current count AND TSC before PIT starts
	MOVQ	$LAPIC_BASE, R8
	MOVL	LAPIC_CURRENT_COUNT(R8), R9	// R9 = LAPIC count at start

	BYTE $0x0F; BYTE $0x31		// RDTSC → EDX:EAX
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, R11			// R11 = TSC at start

	// Enable PIT CH2 gate (start counting)
	MOVW	$0x61, DX
	INB
	ORB	$0x01, AX		// Set bit 0 (gate on)
	OUTB

	// Poll PIT CH2 output (bit 5 of port 0x61) until terminal count
pit_poll:
	MOVW	$0x61, DX
	INB
	TESTB	$0x20, AX		// Test OUT2 (bit 5)
	JZ	pit_poll

	// PIT expired (~10ms elapsed) — read TSC and LAPIC current count
	BYTE $0x0F; BYTE $0x31		// RDTSC → EDX:EAX
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, R12			// R12 = TSC at end

	MOVQ	$LAPIC_BASE, R8
	MOVL	LAPIC_CURRENT_COUNT(R8), R10	// R10 = LAPIC count at end

	// Stop the LAPIC timer (zero initial count)
	MOVL	$0, LAPIC_INITIAL_COUNT(R8)

	// --- Compute elapsed ticks ---
	// TSC counts UP: tscElapsed = end - start
	MOVQ	R12, AX
	SUBQ	R11, AX			// AX = TSC ticks in ~10ms
	MOVQ	AX, R13			// R13 = tscElapsed (save for freq calc)

	// Store tscElapsed to Go variable
	LEAQ	·tscElapsed(SB), BX
	MOVQ	AX, (BX)

	// LAPIC counts DOWN: lapicElapsed = start - end
	MOVL	R9, AX
	SUBL	R10, AX			// AX = LAPIC ticks in ~10ms (32-bit)
	MOVQ	AX, R14			// R14 = lapicElapsed (zero-extended)

	// Store lapicElapsed to Go variable
	LEAQ	·lapicElapsed(SB), BX
	MOVQ	R14, (BX)

	// --- Compute TSC frequency ---
	// TSC freq = tscElapsed × (1,193,182 / 11,932) ≈ tscElapsed × 100
	MOVQ	R13, AX
	MOVQ	$100, BX
	IMULQ	BX, AX			// AX = approximate TSC frequency in Hz

	// Guard against zero or overflow — use lower 32 bits
	// (TSC freq should be ~1GHz under QEMU TCG, fits in uint32)
	TESTL	AX, AX
	JNZ	tsc_cal_ok

	// Fallback: assume TSC = 1 GHz
	MOVL	$1000000000, AX

tsc_cal_ok:
	// Configure LVT Timer for one-shot mode, unmasked (ready for PlatformRearmTimer)
	MOVQ	$LAPIC_BASE, R8
	MOVL	$ONESHOT_VEC30, BX
	MOVL	BX, LAPIC_LVT_TIMER(R8)

	// Return TSC frequency as uint32
	MOVL	AX, ret+0(FP)
	RET

// PlatformReadCounter reads the TSC and returns the current counter value.
// The TSC is still used as the monotonic clock source for timestamps.
// func PlatformReadCounter() uint64
TEXT ·PlatformReadCounter(SB), NOSPLIT, $0-8
	BYTE $0x0F; BYTE $0x31		// RDTSC → EDX:EAX
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, ret+0(FP)
	RET
