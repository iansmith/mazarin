//go:build qemuvirt && aarch64

#include "textflag.h"

// asm_rearmTimer sets the virtual timer TVAL register
// func asm_rearmTimer(ticks uint64)
TEXT ·asm_rearmTimer(SB), NOSPLIT, $0-8
	MOVD	ticks+0(FP), R0
	// MSR CNTV_TVAL_EL0, X0
	// CNTV_TVAL_EL0 = S3_3_C14_C3_0 (op0=3, op1=3, CRn=14, CRm=3, op2=0)
	// Encoding: 0xD51BE300 (NOT 0xD51BE000 which has wrong CRm=0!)
	WORD	$0xD51BE300
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
