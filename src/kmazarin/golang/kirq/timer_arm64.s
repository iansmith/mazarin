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
