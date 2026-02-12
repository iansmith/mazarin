// diplomat/main/uart_riscv64.s
// Direct UART driver for RISC-V 64-bit (NS16550A at 0x10000000)

#include "textflag.h"

// uartPutChar writes a single byte to the UART
// Go signature: func uartPutChar(c byte)
TEXT ·uartPutChar(SB), NOSPLIT, $0-1
	// Load UART base address (0x10000000) into X5
	// LUI X5, 0x10000
	WORD	$0x100002b7

	// Load character from Go stack into X6
	MOVBU	c+0(FP), X6

	// Write character to UART (SB X6, 0(X5))
	WORD	$0x00628023

	RET

// uartDebugMarker writes a single character to UART immediately
// Uses only inline WORD directives (no register allocation needed)
// Go signature: func uartDebugMarker(c byte)
TEXT ·uartDebugMarker(SB), NOSPLIT|NOFRAME, $0-1
	// Load UART base (0x10000000) into X5
	WORD	$0x100002b7  // LUI X5, 0x10000

	// Load character from stack into X6
	MOVBU	c+0(FP), X6

	// Write to UART
	WORD	$0x00628023  // SB X6, 0(X5)

	RET
