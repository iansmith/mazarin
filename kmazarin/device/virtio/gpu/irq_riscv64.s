#include "textflag.h"

// saveAndDisableIRQs reads SSTATUS and atomically clears the SIE bit (bit 1).
// Returns the original SSTATUS value.
// func saveAndDisableIRQs() uint64
TEXT ·saveAndDisableIRQs(SB), NOSPLIT, $0-8
	// CSRRCI A0, sstatus, 2 (read old value, clear SIE bit)
	// Encoding: csr=0x100, uimm=0x2, rd=A0 (x10), funct3=0x7, opcode=0x73
	WORD	$0x10017573
	MOV	A0, ret+0(FP)
	RET

// restoreIRQs restores SSTATUS to a previously saved value.
// func restoreIRQs(daif uint64)
TEXT ·restoreIRQs(SB), NOSPLIT, $0-8
	MOV	daif+0(FP), A0
	// CSRW sstatus, A0
	WORD	$0x10051073
	RET
