//go:build qemuvirt && aarch64

#include "textflag.h"

// ============================================================================
// OVERVIEW
// ============================================================================
// MMIO (Memory-Mapped I/O) primitives for volatile hardware register access.
// These functions prevent compiler optimization of memory accesses to ensure
// proper hardware interaction.
//
// Functions:
//   - MmioRead8(addr uintptr) byte - Read 8-bit value from MMIO
//   - MmioRead32(addr uintptr) uint32 - Read 32-bit value from MMIO
//   - MmioWrite8(addr uintptr, val byte) - Write 8-bit value to MMIO
//   - MmioWrite32(addr uintptr, val uint32) - Write 32-bit value to MMIO
//
// WHY NOT DECOMPOSE:
// These are already atomic primitives (2-3 instructions each). Each performs
// exactly one volatile memory operation. This is the minimal implementation.
//
// ABI NOTES:
// - ABI0 (stack-based) functions callable from external Go code
// - Arguments read from FP offsets, returns written to ret+N(FP)

// MmioRead8(addr uintptr) byte
// Reads an 8-bit value from a memory-mapped register
TEXT ·MmioRead8(SB), NOSPLIT, $0-9
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVB	(R0), R0		// Read byte from address
	MOVB	R0, ret+8(FP)		// Store return value
	RET

// MmioRead32(addr uintptr) uint32
// Reads a 32-bit value from a memory-mapped register
TEXT ·MmioRead32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	(R0), R0		// Read 32-bit value from address
	MOVW	R0, ret+8(FP)		// Store return value
	RET

// MmioWrite8(addr uintptr, val byte)
// Writes an 8-bit value to a memory-mapped register
TEXT ·MmioWrite8(SB), NOSPLIT, $0-9
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVB	val+8(FP), R1		// Load byte value from stack
	MOVB	R1, (R0)		// Write byte to address
	RET

// MmioWrite32(addr uintptr, val uint32)
// Writes a 32-bit value to a memory-mapped register
TEXT ·MmioWrite32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	val+8(FP), R1		// Load value from stack
	MOVW	R1, (R0)		// Write 32-bit value to address
	RET
