#include "textflag.h"

// RawUART writes a single byte to COM1 (I/O port 0x3F8) via OUTB.
// func RawUART(b byte)
TEXT ·RawUART(SB), NOSPLIT, $0-1
	MOVB	b+0(FP), AX
	MOVW	$0x3F8, DX
	OUTB
	RET

// txReady reads COM1 LSR (port 0x3FD) and returns bit 5 (THRE).
// func txReady() bool
TEXT ·txReady(SB), NOSPLIT, $0-1
	MOVW	$0x3FD, DX
	INB
	TESTB	$0x20, AX
	JZ	not_ready
	MOVB	$1, ret+0(FP)
	RET
not_ready:
	MOVB	$0, ret+0(FP)
	RET

// RxReady reads COM1 LSR (port 0x3FD) and returns bit 0 (Data Ready).
// func RxReady() bool
TEXT ·RxReady(SB), NOSPLIT, $0-1
	MOVW	$0x3FD, DX
	INB
	TESTB	$0x01, AX
	JZ	rx_not_ready
	MOVB	$1, ret+0(FP)
	RET
rx_not_ready:
	MOVB	$0, ret+0(FP)
	RET

// ReadRxByte reads one byte from COM1 RBR (port 0x3F8).
// func ReadRxByte() byte
TEXT ·ReadRxByte(SB), NOSPLIT, $0-1
	MOVW	$0x3F8, DX
	INB
	MOVB	AX, ret+0(FP)
	RET

// EnableRxInterrupt enables the Received Data Available interrupt on COM1.
// Writes 0x01 to the IER register (port 0x3F9).
// func EnableRxInterrupt()
TEXT ·EnableRxInterrupt(SB), NOSPLIT, $0-0
	MOVW	$0x3F9, DX	// IER register
	MOVB	$0x01, AX	// Enable Received Data Available interrupt
	OUTB
	RET
