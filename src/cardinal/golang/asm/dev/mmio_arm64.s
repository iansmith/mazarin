#include "textflag.h"

// lib_mmio.s - MMIO (Memory-Mapped I/O) functions in Go/Plan9 assembly
// These provide low-level memory-mapped register access for device drivers
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

// mmio_write(reg uintptr, data uint32)
// Writes a 32-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 32-bit value to write (uint32, in lower 32 bits)
TEXT mmio_write(SB), NOSPLIT|NOFRAME, $0-12
	// R0 = register address (already in R0)
	// R1 = 32-bit data value (already in R1)
	MOVW	R1, (R0)		// Store 32-bit value to address
	RET

// mmio_read(reg uintptr) uint32
// Reads a 32-bit value from a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
// Returns:
//   R0: 32-bit value read (uint32)
TEXT mmio_read(SB), NOSPLIT|NOFRAME, $0-12
	// R0 = register address (already in R0)
	MOVW	(R0), R0		// Load 32-bit value from address into R0
	// Return value is in R0 (register ABI)
	RET

// mmio_write16(reg uintptr, data uint16)
// Writes a 16-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 16-bit value to write (uint16, zero-extended)
TEXT mmio_write16(SB), NOSPLIT|NOFRAME, $0-10
	// R0 = register address (already in R0)
	// R1 = 16-bit data value (already in R1)
	MOVH	R1, (R0)		// Store 16-bit value to address
	RET

// mmio_read16(reg uintptr) uint16
// Reads a 16-bit value from a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
// Returns:
//   R0: 16-bit value read (uint16, zero-extended)
TEXT mmio_read16(SB), NOSPLIT|NOFRAME, $0-10
	// R0 = register address (already in R0)
	MOVHU	(R0), R0		// Load 16-bit value from address (zero-extended)
	// Return value is in R0 (register ABI)
	RET

// mmio_write64(reg uintptr, data uint64)
// Writes a 64-bit value to a memory-mapped register
// Parameters (register ABI):
//   R0: register address (uintptr)
//   R1: 64-bit value to write
TEXT mmio_write64(SB), NOSPLIT|NOFRAME, $0-16
	// R0 = register address (already in R0)
	// R1 = 64-bit data value (already in R1)
	MOVD	R1, (R0)		// Store 64-bit value to address
	RET

// store_pointer_nobarrier(dest uintptr, value uintptr)
// Stores a pointer without triggering Go's write barrier
// Used for low-level pointer manipulation before GC is initialized
// Parameters (register ABI):
//   R0: destination address (uintptr)
//   R1: pointer value to store (uintptr)
TEXT store_pointer_nobarrier(SB), NOSPLIT|NOFRAME, $0-16
	// R0 = destination address (already in R0)
	// R1 = pointer value (already in R1)
	MOVD	R1, (R0)		// Store pointer directly
	RET
