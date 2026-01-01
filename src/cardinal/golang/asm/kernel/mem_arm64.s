// mem_arm64.s - Memory Operation Functions
//
// This file contains memory operation functions like bzero and memmove.
// These are low-level, architecture-optimized memory routines.
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Parameters arrive in R0, R1, etc. Return values go in R0.

#include "textflag.h"

// ============================================================================
// Memory Functions
// ============================================================================

// bzero(ptr unsafe.Pointer, size uint32)
// Zeroes size bytes starting at ptr
// OPTIMIZED: Uses 128-bit stores (STP) for 16x speedup
//
// NOTE: Go's internal ABI passes arguments in REGISTERS (x0, w1), not on stack.
// The linkname directive connects asm.Bzero to this function.
// When called from Go, arguments arrive in: R0 = ptr, R1 = size (lower 32 bits)
// We do NOT use FP-relative addressing since that doesn't work with Go's ABI.
TEXT bzero(SB), NOSPLIT|NOFRAME, $0-12
	// Arguments arrive in registers: R0 = ptr, R1 = size
	// No need to load from stack - Go's register ABI passes them directly!

	// R0 = ptr, R1 = size (already in registers from Go ABI)
	CBZ	R1, bzero_done
	MOVD	ZR, R2			// Zero value
	MOVD	ZR, R3			// Zero value (for pair store)

bzero_loop_16:
	CMP	$16, R1
	BLT	bzero_loop_8
	STP	(R2, R3), (R0)
	ADD	$16, R0
	SUB	$16, R1
	B	bzero_loop_16

bzero_loop_8:
	CMP	$8, R1
	BLT	bzero_loop_1
	MOVD	R2, (R0)
	ADD	$8, R0
	SUB	$8, R1
	B	bzero_loop_8

bzero_loop_1:
	CBZ	R1, bzero_done
	MOVB	R2, (R0)
	ADD	$1, R0
	SUB	$1, R1
	B	bzero_loop_1

bzero_done:
	RET

// MemmoveBytes(dest unsafe.Pointer, src unsafe.Pointer, n uint32)
// Copy n bytes from src to dest
// Optimized for speed using 16-byte chunks
//
// NOTE: Go 1.17+ register-based ABI:
//   R0 = dest pointer
//   R1 = src pointer
//   R2 = n (lower 32 bits used)
TEXT MemmoveBytes(SB), NOSPLIT|NOFRAME, $0-20
	// Arguments already in registers from Go ABI:
	// R0 = dest, R1 = src, R2 = size (as uint32)
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
