#include "textflag.h"

// breadcrumb writes a single byte to COM1 (I/O port 0x3F8).
// Polls the Line Status Register (port 0x3FD) bit 5 (THRE) before writing.
//
// func breadcrumb(b byte)
TEXT ·breadcrumb(SB), NOSPLIT, $0-1
	// Poll LSR until transmitter holding register is empty
poll:
	MOVW	$0x3FD, DX		// LSR port = COM1 base + 5
	INB
	TESTB	$0x20, AX		// bit 5 = THRE
	JZ	poll

	// Write byte to THR
	MOVB	b+0(FP), AX
	MOVW	$0x3F8, DX		// THR port = COM1 base
	OUTB
	RET
