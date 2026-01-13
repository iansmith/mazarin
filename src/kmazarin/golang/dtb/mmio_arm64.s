//go:build qemuvirt && aarch64

#include "textflag.h"

// mmio_read32(reg uintptr) uint32
// Reads a 32-bit value from a memory-mapped register
TEXT ·mmio_read32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	(R0), R0		// Read 32-bit value from address
	MOVW	R0, ret+8(FP)		// Store return value
	RET

// mmio_write32(reg uintptr, data uint32)
// Writes a 32-bit value to a memory-mapped register
TEXT ·mmio_write32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	val+8(FP), R1		// Load value from stack
	MOVW	R1, (R0)		// Write 32-bit value to address
	RET
