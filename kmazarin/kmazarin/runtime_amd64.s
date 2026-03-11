#include "textflag.h"

// readTTBR0 reads the CR3 register (page table base on x86_64).
// func readTTBR0() uint64
TEXT ·readTTBR0(SB), NOSPLIT, $0-8
	MOVQ	CR3, AX
	MOVQ	AX, ret+0(FP)
	RET

// dsb performs a full memory fence (MFENCE on x86_64).
// func dsb()
TEXT ·dsb(SB), NOSPLIT, $0-0
	MFENCE
	RET

// isb performs a serializing instruction (LFENCE on x86_64).
// func isb()
TEXT ·isb(SB), NOSPLIT, $0-0
	LFENCE
	RET

// invalidateTLB invalidates all TLB entries by reloading CR3.
// func invalidateTLB()
TEXT ·invalidateTLB(SB), NOSPLIT, $0-0
	MOVQ	CR3, AX
	MOVQ	AX, CR3		// Reload CR3 flushes TLB
	RET
