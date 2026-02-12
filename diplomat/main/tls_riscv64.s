// diplomat/main/tls_riscv64.s - RISC-V TLS helpers

#include "textflag.h"

// readTP reads the thread pointer register (TP/X4)
// RISC-V uses TP for thread-local storage
TEXT main·readTP(SB), NOSPLIT|NOFRAME, $0-8
	MOV	TP, A0			// A0 = TP (X4)
	MOV	A0, ret+0(FP)
	RET

// writeTP writes the thread pointer register (TP/X4)
TEXT main·writeTP(SB), NOSPLIT|NOFRAME, $0-8
	MOV	val+0(FP), A0
	MOV	A0, TP			// TP = A0
	RET

// debugPortOut writes a byte to UART (NS16550A at 0x10000000)
// RISC-V doesn't have a debug port like x86_64, so we use UART
// Go: func debugPortOut(c byte)
TEXT ·debugPortOut(SB), NOSPLIT, $0-1
	// Load UART base address (0x10000000) into X5
	// LUI X5, 0x10000
	WORD	$0x100002b7

	// Load character from Go stack into X6
	MOVBU	c+0(FP), X6

	// Write character to UART (SB X6, 0(X5))
	WORD	$0x00628023

	RET
