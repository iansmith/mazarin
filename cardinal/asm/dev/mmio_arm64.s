#include "textflag.h"

// lib_mmio.s - MMIO (Memory-Mapped I/O) functions in Go/Plan9 assembly
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains atomic MMIO primitives that cannot be decomposed further.
// Each function is a single-purpose memory-mapped I/O operation that must
// execute as inline assembly to prevent compiler optimization.
//
// Functions:
//   - mmio_write(addr, data) - Write 32-bit value to MMIO register
//   - mmio_read(addr) - Read 32-bit value from MMIO register
//   - mmio_write16(addr, data) - Write 16-bit value to MMIO register
//   - mmio_read16(addr) - Read 16-bit value from MMIO register
//   - mmio_write64(addr, data) - Write 64-bit value to MMIO register
//   - store_pointer_nobarrier(dest, value) - Store pointer without write barrier
//
// ABI NOTES:
// - These functions use Go 1.17+ register-based calling convention
// - Parameters arrive in R0, R1, etc.
// - Return values go in R0
// - Assembly implementations prevent compiler from optimizing away volatile access

// ============================================================================
// mmio_write(reg uintptr, data uint32) - Write 32-bit MMIO Register
// ============================================================================
// Writes a 32-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 32-bit value to write (uint32, in lower 32 bits)
//
// Segments:
//   1. Store 32-bit value to MMIO address
//   2. Return to caller
//
TEXT mmio_write(SB), NOSPLIT|NOFRAME, $0-12
	// For ABI0 compatibility, the wrapper stores args on stack
	// Input: address at 8(RSP), data at 16(RSP)
	// No return value

	// Load address and data from stack
	MOVD	8(RSP), R0		// Load address argument from stack
	MOVW	16(RSP), R1		// Load 32-bit data from stack

	// Store 32-bit value to MMIO address
	MOVW	R1, (R0)		// Store 32-bit value to address

	RET

// ============================================================================
// mmio_read(reg uintptr) - Read 32-bit MMIO Register
// ============================================================================
// Reads a 32-bit value from a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
// Returns:
//   R0: 32-bit value read (uint32)
//
// Segments:
//   1. Load 32-bit value from MMIO address
//   2. Return to caller
//
TEXT mmio_read(SB), NOSPLIT|NOFRAME, $0-12
	// For ABI0 compatibility, the wrapper stores args on stack and reads return from stack
	// Input: address at 8(RSP)
	// Output: must store result at 16(RSP) - this is 8 bytes after the 8-byte input

	// Load address from stack
	MOVD	8(RSP), R0		// Load address argument from stack

	// Load 32-bit value from MMIO address
	MOVWU	(R0), R0		// Load unsigned 32-bit value (zero-extend to 64-bit)

	// Store result to stack at return location
	MOVW	R0, 16(RSP)		// Store 32-bit result at return location

	RET

// ============================================================================
// mmio_write16(reg uintptr, data uint16) - Write 16-bit MMIO Register
// ============================================================================
// Writes a 16-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 16-bit value to write (uint16, zero-extended)
//
// Segments:
//   1. Store 16-bit value to MMIO address
//   2. Return to caller
//
TEXT mmio_write16(SB), NOSPLIT|NOFRAME, $0-10
	// For ABI0 compatibility, the wrapper stores args on stack
	// Input: address at 8(RSP), data at 16(RSP)
	// No return value

	// Load address and data from stack
	MOVD	8(RSP), R0		// Load address argument from stack
	MOVH	16(RSP), R1		// Load 16-bit data from stack

	// Store 16-bit value to MMIO address
	MOVH	R1, (R0)		// Store 16-bit value to address

	RET

// ============================================================================
// mmio_read16(reg uintptr) - Read 16-bit MMIO Register
// ============================================================================
// Reads a 16-bit value from a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
// Returns:
//   R0: 16-bit value read (uint16, zero-extended)
//
// Segments:
//   1. Load 16-bit value from MMIO address
//   2. Return to caller
//
TEXT mmio_read16(SB), NOSPLIT|NOFRAME, $0-10
	// For ABI0 compatibility, the wrapper stores args on stack and reads return from stack
	// Input: address at 8(RSP)
	// Output: must store result at 16(RSP)

	// Load address from stack
	MOVD	8(RSP), R0		// Load address argument from stack

	// Load 16-bit value from MMIO address
	MOVHU	(R0), R0		// Load unsigned 16-bit value (zero-extended to 64-bit)

	// Store result to stack at return location
	MOVH	R0, 16(RSP)		// Store 16-bit result at return location

	RET

// ============================================================================
// mmio_write64(reg uintptr, data uint64) - Write 64-bit MMIO Register
// ============================================================================
// Writes a 64-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 64-bit value to write
//
// Segments:
//   1. Store 64-bit value to MMIO address
//   2. Return to caller
//
TEXT mmio_write64(SB), NOSPLIT|NOFRAME, $0-16
	// For ABI0 compatibility, the wrapper stores args on stack
	// Input: address at 8(RSP), data at 16(RSP)
	// No return value

	// Load address and data from stack
	MOVD	8(RSP), R0		// Load address argument from stack
	MOVD	16(RSP), R1		// Load 64-bit data from stack

	// Store 64-bit value to MMIO address
	MOVD	R1, (R0)		// Store 64-bit value to address

	RET

// ============================================================================
// store_pointer_nobarrier(dest uintptr, value uintptr) - Store Pointer Without Barrier
// ============================================================================
// Stores a pointer without triggering Go's write barrier
// Used for low-level pointer manipulation before GC is initialized
// Parameters (register ABI):
//   R0: destination address (uintptr)
//   R1: pointer value to store (uintptr)
//
// Segments:
//   1. Store pointer value to destination
//   2. Return to caller
//
TEXT store_pointer_nobarrier(SB), NOSPLIT|NOFRAME, $0-16
	// For ABI0 compatibility, the wrapper stores args on stack
	// Input: dest at 8(RSP), value at 16(RSP)
	// No return value

	// Load destination and value from stack
	MOVD	8(RSP), R0		// Load destination address from stack
	MOVD	16(RSP), R1		// Load pointer value from stack

	// Store pointer directly (no write barrier)
	MOVD	R1, (R0)		// Store pointer directly

	RET
