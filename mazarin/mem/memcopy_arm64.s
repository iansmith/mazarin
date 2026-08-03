// memcopy_linux_arm64.s — fast userspace memory copy with cache coherency barrier.
//
// Uses 64-byte unrolled MOVD pairs for throughput, with a trailing DMB ISH
// (Data Memory Barrier, Inner Shareable) so all stores are visible to other
// cores/goroutines before the function returns.

#include "textflag.h"

// func MemCopy(dst, src unsafe.Pointer, n int64)
TEXT ·MemCopy(SB), NOSPLIT, $0-24
	MOVD	dst+0(FP), R0		// dst
	MOVD	src+8(FP), R1		// src
	MOVD	n+16(FP), R2		// n

	CMP	$0, R2
	BLE	done

	// Main loop: copy 64 bytes per iteration (8 × MOVD load, 8 × MOVD store).
	// Reads are grouped before writes so the pipeline can overlap memory ops.
	CMP	$64, R2
	BLT	tail32

loop64:
	MOVD	0(R1), R3
	MOVD	8(R1), R4
	MOVD	16(R1), R5
	MOVD	24(R1), R6
	MOVD	32(R1), R7
	MOVD	40(R1), R8
	MOVD	48(R1), R9
	MOVD	56(R1), R10
	MOVD	R3, 0(R0)
	MOVD	R4, 8(R0)
	MOVD	R5, 16(R0)
	MOVD	R6, 24(R0)
	MOVD	R7, 32(R0)
	MOVD	R8, 40(R0)
	MOVD	R9, 48(R0)
	MOVD	R10, 56(R0)
	ADD	$64, R0
	ADD	$64, R1
	SUB	$64, R2
	CMP	$64, R2
	BGE	loop64

tail32:
	CMP	$32, R2
	BLT	tail16
	MOVD	0(R1), R3
	MOVD	8(R1), R4
	MOVD	16(R1), R5
	MOVD	24(R1), R6
	MOVD	R3, 0(R0)
	MOVD	R4, 8(R0)
	MOVD	R5, 16(R0)
	MOVD	R6, 24(R0)
	ADD	$32, R0
	ADD	$32, R1
	SUB	$32, R2

tail16:
	CMP	$16, R2
	BLT	tail8
	MOVD	0(R1), R3
	MOVD	8(R1), R4
	MOVD	R3, 0(R0)
	MOVD	R4, 8(R0)
	ADD	$16, R0
	ADD	$16, R1
	SUB	$16, R2

tail8:
	CMP	$8, R2
	BLT	tail_bytes
	MOVD	0(R1), R3
	MOVD	R3, 0(R0)
	ADD	$8, R0
	ADD	$8, R1
	SUB	$8, R2

tail_bytes:
	CMP	$0, R2
	BLE	barrier

byte_loop:
	MOVBU	0(R1), R3
	MOVB	R3, 0(R0)
	ADD	$1, R0
	ADD	$1, R1
	SUB	$1, R2
	CMP	$0, R2
	BGT	byte_loop

barrier:
	// DMB ISH — Data Memory Barrier, Inner Shareable.
	// Ensures all preceding stores are visible to any observer in the
	// inner-shareable domain before this function returns.
	DMB	$11			// DMB ISH

done:
	RET
