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
	// Segment 1: Store to MMIO register
	// R0 = register address (already in R0)
	// R1 = 32-bit data value (already in R1)
	MOVW	R1, (R0)		// Store 32-bit value to address

	// Segment 2: Return
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
	// Segment 1: Load from MMIO register
	// R0 = register address (already in R0)
	MOVW	(R0), R0		// Load 32-bit value from address into R0

	// Segment 2: Return
	// Return value is in R0 (register ABI)
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
	// Segment 1: Store to MMIO register
	// R0 = register address (already in R0)
	// R1 = 16-bit data value (already in R1)
	MOVH	R1, (R0)		// Store 16-bit value to address

	// Segment 2: Return
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
	// Segment 1: Load from MMIO register
	// R0 = register address (already in R0)
	MOVHU	(R0), R0		// Load 16-bit value from address (zero-extended)

	// Segment 2: Return
	// Return value is in R0 (register ABI)
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
	// Segment 1: Store to MMIO register
	// R0 = register address (already in R0)
	// R1 = 64-bit data value (already in R1)
	MOVD	R1, (R0)		// Store 64-bit value to address

	// Segment 2: Return
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
	// Segment 1: Store pointer
	// R0 = destination address (already in R0)
	// R1 = pointer value (already in R1)
	MOVD	R1, (R0)		// Store pointer directly

	// Segment 2: Return
	RET
