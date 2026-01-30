#include "textflag.h"

// func dsb()
TEXT ·dsb(SB), NOSPLIT, $0-0
	DSB $15  // DSB SY
	RET

// func isb()
TEXT ·isb(SB), NOSPLIT, $0-0
	ISB $15  // ISB SY
	RET
