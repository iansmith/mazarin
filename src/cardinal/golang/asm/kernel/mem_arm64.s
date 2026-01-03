// mem_arm64.s - Memory Operation Functions
//
// This file contains memory operation functions like bzero and memmove.
// These are low-level, architecture-optimized memory routines.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

// ============================================================================
// Cache Line Operations
// ============================================================================

// dc_zva zeros a cache line at the given address
// R0 = address (must be cache-line aligned)
TEXT dc_zva(SB), NOSPLIT|NOFRAME, $0-0
	WORD	$0xd50b7420	// dc zva, x0
	RET

// ============================================================================
// Memory Functions
// ============================================================================
//
// NOTE: bzero is now implemented as a pure Go function in mmu.go
// using DC ZVA instruction for cache-line-aligned zeroing.
// The old assembly implementation has been removed.

// MemmoveBytes(dest unsafe.Pointer, src unsafe.Pointer, n uint32)
// Copy n bytes from src to dest
// Optimized for speed using 16-byte chunks
//
// NOTE: Go 1.17+ register-based ABI:
//   R0 = dest pointer
//   R1 = src pointer
//   R2 = n (lower 32 bits used)
TEXT MemmoveBytes(SB), NOSPLIT|NOFRAME, $0-0
	// Register ABI (ABIInternal): Arguments passed in registers
	// R0 = dest (unsafe.Pointer)
	// R1 = src (unsafe.Pointer)
	// W2 = n (uint32, lower 32 bits of R2)
	// Signature $0-0 means: no local frame, no stack args (pure register ABI)
	CBZ	R2, memmove_done

	// Check if we can do 16-byte copies
	CMP	$16, R2
	BLT	memmove_bytes

memmove_16:
	LDP	(R1), (R3, R4)		// Load 16 bytes
	ADD	$16, R1
	STP	(R3, R4), (R0)		// Store 16 bytes
	ADD	$16, R0
	SUB	$16, R2
	CMP	$16, R2
	BGE	memmove_16

memmove_bytes:
	CBZ	R2, memmove_done
	MOVBU	(R1), R3
	ADD	$1, R1
	MOVB	R3, (R0)
	ADD	$1, R0
	SUB	$1, R2
	B	memmove_bytes

memmove_done:
	RET
