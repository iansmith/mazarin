// [MAZ-179 probe — NOT FOR MERGE, tier 17]
// Bounds of the exception-vector handler blob for the mislabeled-continuation
// witness. GetExceptionVectorBase directly follows ExceptionVectorTable in
// exceptions_arm64.s, so [lo, hi) covers exactly the vector table plus all
// handler code and nothing else — layout-proof across builds.

#include "textflag.h"

// func maz179VTBounds() (lo, hi uintptr)
TEXT ·maz179VTBounds(SB), NOSPLIT, $0-16
	MOVD	$·ExceptionVectorTable(SB), R0
	MOVD	R0, lo+0(FP)
	MOVD	$·GetExceptionVectorBase(SB), R0
	MOVD	R0, hi+8(FP)
	RET
