//go:build !test_stubs


#include "textflag.h"

// asm_rearmTimer sets the virtual timer using absolute compare value (like Cardinal)
// func asm_rearmTimer(ticks uint64)
TEXT ·asm_rearmTimer(SB), NOSPLIT, $0-8
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

// asm_readCntfrqEl0 reads the counter frequency register
// func asm_readCntfrqEl0() uint32
TEXT ·asm_readCntfrqEl0(SB), NOSPLIT, $0-4
	// MRS X0, CNTFRQ_EL0
	WORD	$0xD53BE000
	MOVW	R0, ret+0(FP)
	RET

// asm_readCntvctEl0 reads the virtual timer counter value
// func asm_readCntvctEl0() uint64
TEXT ·asm_readCntvctEl0(SB), NOSPLIT, $0-8
	// MRS X0, CNTVCT_EL0
	// CNTVCT_EL0 = S3_3_C14_C0_2 (op0=3, op1=3, CRn=14, CRm=0, op2=2)
	WORD	$0xD53BE040
	MOVD	R0, ret+0(FP)
	RET
