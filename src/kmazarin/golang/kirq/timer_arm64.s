//go:build qemuvirt && aarch64

#include "textflag.h"

// asm_rearmTimer sets the virtual timer TVAL register
// func asm_rearmTimer(ticks uint64)
TEXT ·asm_rearmTimer(SB), NOSPLIT, $0-8
	MOVD	ticks+0(FP), R0
	// MSR CNTV_TVAL_EL0, X0 = 0xD51BE000
	WORD	$0xD51BE000
	RET
