// spinlock_riscv64.s - RISC-V assembly for spinlock support

#include "textflag.h"

// yieldProcessor executes a hint instruction for spinlock waiting.
// On RISC-V, we use a FENCE instruction as a memory ordering hint.
// This doesn't provide the same power-saving as ARM64's WFE, but it's
// safe and provides a memory ordering point in the spin loop.
//
// Alternative approaches considered:
// - WFI (Wait For Interrupt): May trap in some privilege configurations
// - PAUSE hint: Not universally available in RV64I baseline
// - FENCE: Always safe, provides memory ordering
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	// FENCE iorw, iorw - Full memory fence
	// Encoding: fm=0000, pred=1111 (iorw), succ=1111 (iorw), rs1=0, funct3=000, rd=0, opcode=0x0F
	// = 0x0FF0000F
	WORD $0x0FF0000F
	RET
