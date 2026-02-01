#include "textflag.h"

// saveAndDisableIRQs saves DAIF and masks all interrupts.
// func saveAndDisableIRQs() uint64
TEXT ·saveAndDisableIRQs(SB), NOSPLIT, $0-8
	MRS	DAIF, R0
	MSR	$0xf, DAIFSet  // Mask D, A, I, F
	MOVD	R0, ret+0(FP)
	RET

// restoreIRQs restores DAIF to a previously saved value.
// func restoreIRQs(daif uint64)
TEXT ·restoreIRQs(SB), NOSPLIT, $0-8
	MOVD	daif+0(FP), R0
	MSR	R0, DAIF
	RET
