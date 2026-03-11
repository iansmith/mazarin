#include "textflag.h"

// readTTBR0 reads the satp CSR (page table base on RISC-V).
// func readTTBR0() uint64
TEXT ·readTTBR0(SB), NOSPLIT, $0-8
	// CSRR A0, satp
	WORD	$0x18002573		// csrr a0, satp
	MOV	A0, ret+0(FP)
	RET

// dsb performs a full memory fence (FENCE on RISC-V).
// func dsb()
TEXT ·dsb(SB), NOSPLIT, $0-0
	// FENCE rw,rw
	WORD	$0x0330000F
	RET

// isb flushes the instruction pipeline (FENCE.I on RISC-V).
// func isb()
TEXT ·isb(SB), NOSPLIT, $0-0
	// FENCE.I
	WORD	$0x0000100F
	RET

// invalidateTLB invalidates all TLB entries.
// func invalidateTLB()
TEXT ·invalidateTLB(SB), NOSPLIT, $0-0
	// FENCE rw,rw
	WORD	$0x0330000F
	// SFENCE.VMA zero,zero (flush all TLB entries)
	WORD	$0x12000073
	// FENCE.I
	WORD	$0x0000100F
	RET
