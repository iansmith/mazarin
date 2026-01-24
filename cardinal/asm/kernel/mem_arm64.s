// mem_arm64.s - Memory Operation Primitives
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains low-level memory operation primitives.
//
// Active Functions:
//   - dc_zva(addr uintptr) - Zero entire cache line at address
//
// Removed Functions (now implemented in Go):
//   - bzero - Implemented as pure Go function in mmu.go using dc_zva
//   - MemmoveBytes - Implemented in Go (kernel.go) due to assembly issues
//
// WHY NOT DECOMPOSE:
// dc_zva is already an atomic primitive - it's a single ARM64 instruction wrapper.
// This cannot be decomposed further as it performs exactly one hardware operation.
//
// ABI NOTES:
// - These functions use Go 1.17+ register-based calling convention
// - Parameters arrive in R0, R1, etc.
// - dc_zva requires cache-line-aligned address in R0

#include "textflag.h"

// ============================================================================
// dc_zva(addr uintptr) - Zero Cache Line
// ============================================================================
// Zeros an entire cache line at the given address using ARM64 DC ZVA instruction
// This is the most efficient way to zero memory on ARM64 when the address
// is cache-line aligned.
//
// Parameters (register ABI):
//   R0: address (must be cache-line aligned, typically 64 bytes)
//
// Segments:
//   1. Execute DC ZVA instruction to zero cache line
//   2. Return to caller
//
TEXT dc_zva(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Zero cache line
	WORD	$0xd50b7420		// dc zva, x0 (zeros cache line at R0)

	// Segment 2: Return
	RET

// ============================================================================
// Memory Functions
// ============================================================================
//
// NOTE: bzero is now implemented as a pure Go function in mmu.go
// using DC ZVA instruction for cache-line-aligned zeroing.
// The old assembly implementation has been removed.
//
// NOTE: MemmoveBytes is now implemented in Go (kernel.go)
// The assembly version was hanging - commented out below

// ASSEMBLY VERSION COMMENTED OUT - Using Go implementation in kernel.go
// MemmoveBytes(dest unsafe.Pointer, src unsafe.Pointer, n uint32)
// Copy n bytes from src to dest
// Simple version using 8-byte copies (no LDP/STP to avoid alignment faults)
//
// NOTE: Go 1.17+ register-based ABI:
//   R0 = dest pointer
//   R1 = src pointer
//   R2 = n (lower 32 bits used)
// TEXT MemmoveBytes(SB), NOSPLIT|NOFRAME, $0-0
// 	// Register ABI (ABIInternal): Arguments passed in registers
// 	// R0 = dest (unsafe.Pointer)
// 	// R1 = src (unsafe.Pointer)
// 	// W2 = n (uint32, lower 32 bits of R2)
// 	CBZ	R2, memmove_done
//
// 	// Copy 8 bytes at a time if possible
// 	CMP	$8, R2
// 	BLT	memmove_bytes
//
// memmove_8:
// 	// TEST: Try just the byte-by-byte loop to see if 8-byte access is the issue
// 	// Skip the MOVD and fall through to byte loop
// 	B	memmove_bytes
//
// 	MOVD	(R1), R3		// Load 8 bytes (UNREACHABLE for now)
// 	MOVD	R3, (R0)		// Store 8 bytes
// 	ADD	$8, R1
// 	ADD	$8, R0
// 	SUB	$8, R2
// 	CMP	$8, R2
// 	BGE	memmove_8
//
// memmove_bytes:
// 	CBZ	R2, memmove_done
// 	// DEBUG: The fact that we reach here means loop started, but we can't print
// 	// from assembly easily. Just try a simple NOP to see if we get here
// 	MOVBU	(R1), R3		// Load 1 byte (unsigned)
// 	MOVB	R3, (R0)		// Store 1 byte
// 	ADD	$1, R1
// 	ADD	$1, R0
// 	SUB	$1, R2
// 	// Check if we should continue (this was missing!)
// 	CBNZ	R2, memmove_bytes	// Branch if R2 != 0
//
// memmove_done:
// 	RET
