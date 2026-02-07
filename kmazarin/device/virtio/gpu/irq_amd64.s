#include "textflag.h"

// saveAndDisableIRQs saves RFLAGS and disables interrupts (CLI).
// func saveAndDisableIRQs() uint64
TEXT ·saveAndDisableIRQs(SB), NOSPLIT, $0-8
	PUSHFQ
	POPQ	AX
	CLI
	MOVQ	AX, ret+0(FP)
	RET

// restoreIRQs restores RFLAGS to a previously saved value.
// func restoreIRQs(daif uint64)
TEXT ·restoreIRQs(SB), NOSPLIT, $0-8
	MOVQ	daif+0(FP), AX
	PUSHQ	AX
	POPFQ
	RET
