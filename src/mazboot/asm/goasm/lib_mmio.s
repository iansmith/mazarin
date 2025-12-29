#include "textflag.h"

// lib_mmio.s - MMIO (Memory-Mapped I/O) functions in Go/Plan9 assembly
// These provide low-level memory-mapped register access for device drivers

// mmio_write(reg uintptr, data uint32)
// Writes a 32-bit value to a memory-mapped register
// Parameters:
//   reg+0(FP): register address (uintptr)
//   data+8(FP): 32-bit value to write (uint32, in lower 32 bits)
TEXT mmio_write(SB), NOSPLIT|NOFRAME, $0-12
	MOVD	reg+0(FP), R0		// R0 = register address
	MOVW	data+8(FP), R1		// R1 = 32-bit data value
	MOVW	R1, (R0)		// Store 32-bit value to address
	RET

// mmio_read(reg uintptr) uint32
// Reads a 32-bit value from a memory-mapped register
// Parameters:
//   reg+0(FP): register address (uintptr)
// Returns:
//   ret+8(FP): 32-bit value read (uint32)
TEXT mmio_read(SB), NOSPLIT|NOFRAME, $0-12
	MOVD	reg+0(FP), R0		// R0 = register address
	MOVW	(R0), R0		// Load 32-bit value from address
	MOVW	R0, ret+8(FP)		// Return value
	RET

// mmio_write16(reg uintptr, data uint16)
// Writes a 16-bit value to a memory-mapped register
// Parameters:
//   reg+0(FP): register address (uintptr)
//   data+8(FP): 16-bit value to write (uint16, zero-extended)
TEXT mmio_write16(SB), NOSPLIT|NOFRAME, $0-10
	MOVD	reg+0(FP), R0		// R0 = register address
	MOVHU	data+8(FP), R1		// R1 = 16-bit data value (zero-extended)
	MOVH	R1, (R0)		// Store 16-bit value to address
	RET

// mmio_read16(reg uintptr) uint16
// Reads a 16-bit value from a memory-mapped register
// Parameters:
//   reg+0(FP): register address (uintptr)
// Returns:
//   ret+8(FP): 16-bit value read (uint16, zero-extended)
TEXT mmio_read16(SB), NOSPLIT|NOFRAME, $0-10
	MOVD	reg+0(FP), R0		// R0 = register address
	MOVHU	(R0), R0		// Load 16-bit value from address (zero-extended)
	MOVH	R0, ret+8(FP)		// Return value
	RET

// mmio_write64(reg uintptr, data uint64)
// Writes a 64-bit value to a memory-mapped register
// Parameters:
//   reg+0(FP): register address (uintptr)
//   data+8(FP): 64-bit value to write
TEXT mmio_write64(SB), NOSPLIT|NOFRAME, $0-16
	MOVD	reg+0(FP), R0		// R0 = register address
	MOVD	data+8(FP), R1		// R1 = 64-bit data value
	MOVD	R1, (R0)		// Store 64-bit value to address
	RET

// store_pointer_nobarrier(dest uintptr, value uintptr)
// Stores a pointer without triggering Go's write barrier
// Used for low-level pointer manipulation before GC is initialized
// Parameters:
//   dest+0(FP): destination address (uintptr)
//   value+8(FP): pointer value to store (uintptr)
TEXT store_pointer_nobarrier(SB), NOSPLIT|NOFRAME, $0-16
	MOVD	dest+0(FP), R0		// R0 = destination address
	MOVD	value+8(FP), R1		// R1 = pointer value
	MOVD	R1, (R0)		// Store pointer directly
	RET
