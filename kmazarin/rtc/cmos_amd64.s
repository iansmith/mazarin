#include "textflag.h"

// cmosRead writes a register index to port 0x70, then reads the value from port 0x71.
// func cmosRead(index uint8) uint8
TEXT ·cmosRead(SB), NOSPLIT, $0-9
	MOVB	index+0(FP), AX
	MOVW	$0x70, DX
	OUTB
	MOVW	$0x71, DX
	INB
	MOVB	AX, ret+8(FP)
	RET

// cmosWrite writes a register index to port 0x70, then writes a value to port 0x71.
// func cmosWrite(index uint8, val uint8)
TEXT ·cmosWrite(SB), NOSPLIT, $0-2
	MOVB	index+0(FP), AX
	MOVW	$0x70, DX
	OUTB
	MOVB	val+1(FP), AX
	MOVW	$0x71, DX
	OUTB
	RET
