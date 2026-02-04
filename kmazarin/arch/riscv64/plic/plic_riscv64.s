//go:build !test_stubs

#include "textflag.h"

// asm_fence performs a full I/O and memory fence.
// Ensures all prior memory and I/O operations complete before subsequent ones.
// Used after MMIO writes that require ordering guarantees.
TEXT ·asm_fence(SB), NOSPLIT, $0-0
	// FENCE iorw,iorw
	WORD	$0x0FF0000F
	RET
