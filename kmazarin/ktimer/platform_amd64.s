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

// PlatformRearmTimer sets the next timer interrupt after 'ticks' LAPIC timer ticks.
// Uses LAPIC one-shot mode: writes initial count, timer counts down to zero and
// fires vector 0x30.
//
// The 'ticks' parameter is in LAPIC timer ticks (at the LAPIC timer frequency,
// which is calibrated by PlatformTimerInit). The caller (ktimer.Rearm) passes
// the value computed from the desired interval and the calibrated frequency.
//
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOVQ	ticks+0(FP), R8		// R8 = LAPIC timer ticks

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

// PlatformTimerInit calibrates the LAPIC timer frequency using PIT Channel 2.
// Returns the LAPIC timer frequency in Hz.
//
// Strategy: Start the LAPIC timer counting down from a large value, use PIT
// to measure ~10ms, then see how many LAPIC ticks elapsed. This gives us the
// LAPIC timer frequency directly, avoiding any TSC-to-LAPIC conversion issues.
//
// func PlatformTimerInit() uint32
TEXT ·PlatformTimerInit(SB), NOSPLIT, $0-4
	// --- Configure LAPIC timer for calibration ---
	// Set divide config to 1 (divide by 1)
	// Divide value encoding: 0xB = divide by 1
	MOVQ	$LAPIC_BASE, R8
	MOVL	$0x0B, AX
	MOVL	AX, LAPIC_DIVIDE_CONFIG(R8)

	// Set LVT timer to one-shot, MASKED (we don't want an interrupt during calibration)
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

	// Read LAPIC current count before PIT starts
	MOVQ	$LAPIC_BASE, R8
	MOVL	LAPIC_CURRENT_COUNT(R8), R9	// R9 = LAPIC count at start

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

	// PIT expired (~10ms elapsed) — read LAPIC current count
	MOVQ	$LAPIC_BASE, R8
	MOVL	LAPIC_CURRENT_COUNT(R8), R10	// R10 = LAPIC count at end

	// Stop the LAPIC timer (zero initial count)
	MOVL	$0, LAPIC_INITIAL_COUNT(R8)

	// LAPIC counts DOWN, so elapsed = start - end
	MOVL	R9, AX
	SUBL	R10, AX			// AX = LAPIC ticks in ~10ms
	// AX is 32-bit result (MOVL/SUBL)

	// Guard against zero (shouldn't happen)
	TESTL	AX, AX
	JNZ	lapic_cal_ok

	// Fallback: assume LAPIC timer = 100 MHz
	MOVL	$100000000, AX
	JMP	lapic_cal_store

lapic_cal_ok:
	// LAPIC freq = LAPIC_elapsed × (1,193,182 / 11,932) ≈ LAPIC_elapsed × 100
	MOVL	$100, BX
	IMULL	BX, AX			// AX = approximate LAPIC timer frequency in Hz

lapic_cal_store:
	// Configure LVT Timer for one-shot mode, unmasked (ready for PlatformRearmTimer)
	MOVQ	$LAPIC_BASE, R8
	MOVL	$ONESHOT_VEC30, BX
	MOVL	BX, LAPIC_LVT_TIMER(R8)

	// Return LAPIC timer frequency as uint32
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
