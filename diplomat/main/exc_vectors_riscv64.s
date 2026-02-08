//go:build riscv64

// diplomat/main/exc_vectors_riscv64.s
// Exception handler for diplomat (RISC-V 64-bit) - SIMPLIFIED VERSION
//
// This is a minimal stub to get diplomat building. Full exception handling
// will need to be implemented with correct Go RISC-V assembler syntax.

#include "textflag.h"

// getDiplomatExceptionHandlerAddr returns the address of diplomatExceptionHandler.
// Go: func getDiplomatExceptionHandlerAddr() uint64
TEXT ·getDiplomatExceptionHandlerAddr(SB), NOSPLIT, $0-8
	MOV	$·diplomatExceptionHandler(SB), A0
	MOV	A0, ret+0(FP)
	RET

// setSTVEC sets STVEC CSR to the given address (Direct mode).
// Go: func setSTVEC(addr uint64)
TEXT ·setSTVEC(SB), NOSPLIT, $0-8
	MOV	addr+0(FP), A0
	// Clear low 2 bits to ensure Direct mode (mode = 0)
	AND	$-4, A0, A0
	// CSRW STVEC, A0
	WORD	$0x10551073	// csrw stvec, a0 (stvec = 0x105)
	RET

// diplomatExceptionHandler - STUB for now
// TODO: Implement full exception handling with correct RISC-V syntax
TEXT ·diplomatExceptionHandler(SB), NOSPLIT|NOFRAME, $0
	// For now, just halt on any exception
	// WFI loop
halt_loop:
	WORD	$0x10500073	// wfi
	JMP	halt_loop
