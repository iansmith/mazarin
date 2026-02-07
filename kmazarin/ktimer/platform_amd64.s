//go:build amd64 && !test_stubs

#include "textflag.h"

// MSR and LAPIC constants
#define IA32_TSC_DEADLINE   0x6E0
#define LAPIC_BASE          0xFEE00000
#define LAPIC_LVT_TIMER     0x320
#define TSC_DEADLINE_MODE   0x00040020  // Bit 18=1 (TSC-Deadline) + vector 0x20

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

// PlatformRearmTimer sets the TSC deadline for the next timer interrupt.
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOVQ	ticks+0(FP), BX  // BX = ticks to add

	// Read current TSC value using RDTSC
	// Returns EDX:EAX (high:low)
	BYTE $0x0F; BYTE $0x31  // RDTSC

	// Combine EDX:EAX into a single 64-bit value in AX
	SHLQ	$32, DX
	ORQ	DX, AX  // AX = current TSC

	// Calculate deadline: current + ticks
	ADDQ	BX, AX  // AX = current + ticks

	// Write deadline to IA32_TSC_DEADLINE MSR (0x6E0)
	// WRMSR expects: ECX = MSR number, EDX:EAX = value
	MOVL	$IA32_TSC_DEADLINE, CX
	MOVQ	AX, DX
	SHRQ	$32, DX  // DX = high 32 bits
	// AX already has low 32 bits
	BYTE $0x0F; BYTE $0x30  // WRMSR

	RET

// PlatformTimerInit configures the LAPIC timer in TSC-Deadline mode.
// func PlatformTimerInit() uint32
TEXT ·PlatformTimerInit(SB), NOSPLIT, $0-4
	// Write to LAPIC LVT Timer Register to enable TSC-Deadline mode
	// LAPIC base: 0xFEE00000 (identity-mapped by bootloader)
	// LVT Timer offset: 0x320
	// Value: 0x00040020 (TSC-Deadline mode + vector 0x20)
	MOVQ	$LAPIC_BASE, AX
	MOVL	$TSC_DEADLINE_MODE, BX
	MOVL	BX, LAPIC_LVT_TIMER(AX)

	// Return TSC frequency: hardcoded to 1GHz for QEMU
	// TODO: Use CPUID leaf 0x15 to calculate actual TSC frequency
	MOVL	$1000000000, AX
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
