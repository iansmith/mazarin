
#include "textflag.h"

// asm_dsb_sy performs a data synchronization barrier (system scope)
// Ensures all memory operations complete before continuing
TEXT ·asm_dsb_sy(SB), NOSPLIT, $0
	DSB	$15	// DSB SY - data synchronization barrier, system scope
	RET

// asm_isb performs an instruction synchronization barrier
// Flushes pipeline and ensures barriers take effect
TEXT ·asm_isb(SB), NOSPLIT, $0
	ISB	$15	// instruction synchronization barrier
	RET
