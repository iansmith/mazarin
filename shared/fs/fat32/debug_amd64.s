// shared/fs/fat32/debug_amd64.s
// Debug port output for FAT32 package

#include "textflag.h"

// func debugOut(c byte)
//
// Writes a byte to QEMU debug port 0xE9
TEXT ·debugOut(SB), NOSPLIT, $0-1
	MOVB c+0(FP), AL
	MOVW $0xE9, DX
	OUTB
	RET
