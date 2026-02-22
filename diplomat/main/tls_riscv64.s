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

// debugPortOut is a no-op on RISC-V.
// On AMD64, this writes to QEMU debug port 0xE9 (invisible on serial).
// On RISC-V, it was writing to UART which polluted serial output.
// Go: func debugPortOut(c byte)
TEXT ·debugPortOut(SB), NOSPLIT, $0-1
	RET
