
#include "textflag.h"

// asm_dsb_sy performs a data synchronization barrier (system scope)
// Ensures all memory operations complete before continuing
TEXT ·asm_dsb_sy(SB), NOSPLIT, $0
	// DSB SY - data synchronization barrier, system scope
	// Encoding: 0xD5033F9F
	WORD	$0xD5033F9F
	RET

// asm_isb performs an instruction synchronization barrier
// Flushes pipeline and ensures barriers take effect
TEXT ·asm_isb(SB), NOSPLIT, $0
	// ISB - instruction synchronization barrier
	// Encoding: 0xD5033FDF
	WORD	$0xD5033FDF
	RET
