//go:build amd64

#include "textflag.h"

// LAPIC constants
#define LAPIC_PHYS_BASE     0xFEE00000
#define KERNEL_MMIO_OFFSET  0xFFFFFFFF00000000
#define LAPIC_BASE          (LAPIC_PHYS_BASE + KERNEL_MMIO_OFFSET)
#define LAPIC_LVT_TIMER     0x320
#define LAPIC_INITIAL_COUNT 0x380
#define LAPIC_CURRENT_COUNT 0x390
#define LAPIC_DIVIDE_CONFIG 0x3E0

// LVT Timer: one-shot mode (bits 18:17 = 00), unmasked (bit 16 = 0), vector 0x30
#define ONESHOT_MODE_VEC30  0x00000030

// PlatformDisableTimer masks the LAPIC timer to stop generating interrupts.
// func PlatformDisableTimer()
TEXT ·PlatformDisableTimer(SB), NOSPLIT, $0-0
	// Read current LVT Timer register
	MOVQ	$LAPIC_BASE, AX
	MOVL	LAPIC_LVT_TIMER(AX), BX
	// Set bit 16 (masked) to disable interrupts
	ORL	$0x10000, BX
	MOVL	BX, LAPIC_LVT_TIMER(AX)
	RET

// PlatformRearmTimer converts TSC ticks to LAPIC ticks and starts the countdown.
// The caller passes ticks in TSC frequency units (matching PlatformReadCounter).
// We scale to LAPIC frequency: lapic_ticks = tsc_ticks * scaleNumer / scaleDenom.
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOVQ	ticks+0(FP), R8		// R8 = TSC ticks to wait

	// Scale TSC ticks to LAPIC ticks using PIT-calibrated ratio
	MOVQ	·scaleDenom(SB), CX
	TESTQ	CX, CX
	JZ	rearm_fallback		// Guard: no calibration data

	MOVQ	R8, AX			// RAX = tsc_ticks
	MOVQ	·scaleNumer(SB), BX
	MULQ	BX			// RDX:RAX = tsc_ticks * scaleNumer
	DIVQ	CX			// RAX = (tsc_ticks * scaleNumer) / scaleDenom

	// Cap to 32-bit max (LAPIC initial count is 32-bit)
	MOVQ	$0xFFFFFFFF, CX
	CMPQ	AX, CX
	JBE	rearm_check_zero
	MOVQ	CX, AX
rearm_check_zero:
	// Ensure at least 1 tick
	TESTQ	AX, AX
	JNZ	rearm_write
	MOVQ	$1, AX
	JMP	rearm_write

rearm_fallback:
	// No calibration data: use ticks directly (wrong frequency but won't crash)
	MOVQ	R8, AX

rearm_write:
	MOVQ	AX, R8			// R8 = LAPIC ticks

	// Ensure divide-by-1 (0x0B) matches PlatformTimerInit calibration.
	// RearmTimerNow sets divide-by-16 (0x03) which makes all subsequent
	// rearms 16x slower than calibrated if we don't reset it here.
	MOVQ	$LAPIC_BASE, AX
	MOVL	$0x0B, CX
	MOVL	CX, LAPIC_DIVIDE_CONFIG(AX)

	// Set LVT Timer: one-shot, unmasked, vector 0x30
	MOVL	$ONESHOT_MODE_VEC30, CX
	MOVL	CX, LAPIC_LVT_TIMER(AX)

	// Write initial count to start countdown
	MOVL	R8, LAPIC_INITIAL_COUNT(AX)
	RET

// PlatformTimerInit configures the LAPIC timer and calibrates frequencies.
// Uses PIT Channel 2 (~10ms gate) as a reference to measure the TSC and
// LAPIC timer rates. Stores the LAPIC/TSC ratio in scaleNumer/scaleDenom
// for PlatformRearmTimer. Returns the TSC frequency in Hz.
//
// Register usage:
//   R8  = LAPIC MMIO base
//   R9  = TSC start value
//   R10 = LAPIC current count at calibration end
//   R11 = TSC end value
//   R12 = LAPIC elapsed ticks
//   R13 = TSC elapsed ticks
//
// func PlatformTimerInit() uint32
TEXT ·PlatformTimerInit(SB), NOSPLIT, $0-4
	MOVQ	$LAPIC_BASE, R8

	// Configure LAPIC divider: divide-by-1 (0x0B)
	MOVL	$0x0B, BX
	MOVL	BX, LAPIC_DIVIDE_CONFIG(R8)

	// Set LVT Timer: one-shot, MASKED during calibration, vector 0x30
	MOVL	$0x00010030, BX
	MOVL	BX, LAPIC_LVT_TIMER(R8)

	// Start LAPIC countdown from 0xFFFFFFFF
	MOVL	$0xFFFFFFFF, BX
	MOVL	BX, LAPIC_INITIAL_COUNT(R8)

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

	// Read TSC start
	BYTE	$0x0F; BYTE $0x31	// RDTSC → EDX:EAX
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, R9			// R9 = TSC start

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

	// PIT expired — read LAPIC current count and TSC end
	MOVL	LAPIC_CURRENT_COUNT(R8), R10	// R10 = LAPIC remaining
	BYTE	$0x0F; BYTE $0x31	// RDTSC
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, R11			// R11 = TSC end

	// Calculate elapsed ticks
	// LAPIC elapsed = 0xFFFFFFFF - current (timer counts DOWN)
	MOVL	$0xFFFFFFFF, AX
	MOVQ	AX, R12			// zero-extend 32→64
	SUBQ	R10, R12		// R12 = LAPIC elapsed

	// TSC elapsed
	MOVQ	R11, R13
	SUBQ	R9, R13			// R13 = TSC elapsed

	// Guard against zero TSC (shouldn't happen)
	TESTQ	R13, R13
	JNZ	cal_ok

	// Fallback: assume ~3:1 LAPIC:TSC ratio, TSC = 1 GHz
	MOVQ	$1, R12
	MOVQ	$3, R13
	MOVL	$1000000000, AX
	JMP	cal_store

cal_ok:
	// TSC freq ≈ TSC_elapsed × (1,193,182 / 11,932) ≈ TSC_elapsed × 100
	// The 0.01% error from rounding 100.01→100 is negligible.
	MOVQ	R13, AX
	MOVQ	$100, BX
	IMULQ	BX, AX			// AX = approximate TSC frequency in Hz

cal_store:
	// Store calibration ratio for PlatformRearmTimer
	MOVQ	R12, ·scaleNumer(SB)	// LAPIC ticks in ~10ms
	MOVQ	R13, ·scaleDenom(SB)	// TSC ticks in ~10ms

	// Stop LAPIC calibration countdown
	MOVL	$0, LAPIC_INITIAL_COUNT(R8)

	// Unmask timer for future use
	MOVL	$ONESHOT_MODE_VEC30, BX
	MOVL	BX, LAPIC_LVT_TIMER(R8)

	// TSC freq already in AX from calibration

	// Return TSC frequency as uint32
	MOVL	AX, ret+0(FP)
	RET

// PlatformReadCounter reads the TSC and returns the current counter value.
// func PlatformReadCounter() uint64
TEXT ·PlatformReadCounter(SB), NOSPLIT, $0-8
	// Read TSC using RDTSC
	// Returns EDX:EAX (high:low)
	BYTE $0x0F; BYTE $0x31  // RDTSC

	// Combine EDX:EAX into a single 64-bit value
	SHLQ	$32, DX
	ORQ	DX, AX  // AX = TSC value

	MOVQ	AX, ret+0(FP)
	RET
