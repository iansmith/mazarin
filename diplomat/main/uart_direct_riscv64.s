// diplomat/main/uart_direct_riscv64.s
// Direct UART write with absolute minimal overhead

#include "textflag.h"

// uartWriteDirect writes one byte to UART with minimal overhead
TEXT ·uartWriteDirect(SB), NOSPLIT|NOFRAME, $0-1
	// LUI X5, 0x10000 (UART base)
	WORD	$0x100002b7
	// Load byte from stack
	MOVBU	c+0(FP), X6
	// SB X6, 0(X5)
	WORD	$0x00628023
	RET
