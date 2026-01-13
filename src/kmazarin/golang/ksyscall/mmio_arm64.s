//go:build qemuvirt && aarch64

#include "textflag.h"

// mmio_write8(reg uintptr, data byte)
// Writes an 8-bit value to a memory-mapped register
TEXT ·mmio_write8(SB), NOSPLIT, $0-9
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVB	val+8(FP), R1		// Load byte value from stack
	MOVB	R1, (R0)		// Write byte to address
	RET
