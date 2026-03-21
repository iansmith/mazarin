//go:build arm64

#include "textflag.h"

// PlatformDisableTimer disables the ARM virtual timer hardware.
// func PlatformDisableTimer()
TEXT ·PlatformDisableTimer(SB), NOSPLIT, $0
	// Disable timer: CNTV_CTL_EL0 = 0 (disable bit clear)
	MOVD	$0, R0
	// MSR CNTV_CTL_EL0, X0
	// CNTV_CTL_EL0 = S3_3_C14_C3_1 (op0=3, op1=3, CRn=14, CRm=3, op2=1)
	WORD	$0xD51BE320
	ISB	$15
	RET

// PlatformRearmTimer sets the virtual timer using absolute compare value.
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOVD	ticks+0(FP), R0  // R0 = ticks to add

	// Read current counter value from CNTVCT_EL0
	// MRS X1, CNTVCT_EL0
	WORD	$0xD53BE041  // Read into R1

	// Calculate new compare value: current + ticks
	ADD	R0, R1, R1  // R1 = current + ticks

	// Write to CNTV_CVAL_EL0 (absolute compare value)
	// MSR CNTV_CVAL_EL0, X1
	// CNTV_CVAL_EL0 = S3_3_C14_C3_2 (op0=3, op1=3, CRn=14, CRm=3, op2=2)
	WORD	$0xD51BE341  // Write R1 to CVAL

	// Ensure timer is enabled and unmasked
	MOVD	$1, R2
	// MSR CNTV_CTL_EL0, X2
	WORD	$0xD51BE322  // Write R2 to CTL

	ISB	$15
	RET

// PlatformTimerInit reads the counter frequency register and enables
// EL0 access to counter registers (CNTVCT_EL0, CNTFRQ_EL0).
// This is needed for userspace nanotime1/walltime to read the counter
// directly without trapping to EL1.
// func PlatformTimerInit() uint32
TEXT ·PlatformTimerInit(SB), NOSPLIT, $0-4
	// Enable EL0 access to virtual and physical counter registers.
	// CNTKCTL_EL1: bit 0 = EL0PCTEN, bit 1 = EL0VCTEN
	MOVD	$3, R0		// Set both EL0PCTEN and EL0VCTEN
	// MSR CNTKCTL_EL1, X0 (op0=3, op1=0, CRn=14, CRm=1, op2=0)
	WORD	$0xD518E100
	ISB	$15

	// MRS X0, CNTFRQ_EL0
	WORD	$0xD53BE000
	MOVW	R0, ret+0(FP)
	RET

// PlatformReadCounter reads the virtual timer counter value.
// func PlatformReadCounter() uint64
TEXT ·PlatformReadCounter(SB), NOSPLIT, $0-8
	// MRS X0, CNTVCT_EL0
	// CNTVCT_EL0 = S3_3_C14_C0_2 (op0=3, op1=3, CRn=14, CRm=0, op2=2)
	WORD	$0xD53BE040
	MOVD	R0, ret+0(FP)
	RET
