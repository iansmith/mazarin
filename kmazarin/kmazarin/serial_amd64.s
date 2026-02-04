#include "textflag.h"

// ForceSerialCharacter writes a single byte to COM1 (I/O port 0x3F8).
// func ForceSerialCharacter(b byte)
TEXT ·ForceSerialCharacter(SB), NOSPLIT, $0-1
	MOVB	b+0(FP), AX
	MOVW	$0x3F8, DX
	OUTB
	RET
