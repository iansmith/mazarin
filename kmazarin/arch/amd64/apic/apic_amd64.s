//go:build !test_stubs

#include "textflag.h"

// asm_mfence performs a full memory fence.
// Ensures all prior loads and stores complete before subsequent operations.
// Used after MMIO writes that require ordering guarantees.
TEXT ·asm_mfence(SB), NOSPLIT, $0-0
	MFENCE
	RET
