// memcopy_linux_riscv64.s — fast userspace memory copy with memory fence.
//
// Uses 64-byte unrolled doubleword loads/stores for throughput, with a
// trailing FENCE rw,rw so all stores are visible to other harts before the
// function returns.

#include "textflag.h"

// func MemCopy(dst, src unsafe.Pointer, n int64)
TEXT ·MemCopy(SB), NOSPLIT, $0-24
	MOV	dst+0(FP), A0		// dst
	MOV	src+8(FP), A1		// src
	MOV	n+16(FP), A2		// n

	BLE	A2, ZERO, done

	// Main loop: copy 64 bytes per iteration (8 doubleword loads, 8 stores).
	MOV	$64, A3

loop64:
	BLT	A2, A3, tail8
	MOV	0(A1), A4
	MOV	8(A1), A5
	MOV	16(A1), A6
	MOV	24(A1), A7
	MOV	32(A1), T0
	MOV	40(A1), T1
	MOV	48(A1), T2
	MOV	56(A1), T3
	MOV	A4, 0(A0)
	MOV	A5, 8(A0)
	MOV	A6, 16(A0)
	MOV	A7, 24(A0)
	MOV	T0, 32(A0)
	MOV	T1, 40(A0)
	MOV	T2, 48(A0)
	MOV	T3, 56(A0)
	ADD	$64, A0
	ADD	$64, A1
	ADD	$-64, A2
	JMP	loop64

tail8:
	MOV	$8, A3
	BLT	A2, A3, tail_bytes
	MOV	0(A1), A4
	MOV	A4, 0(A0)
	ADD	$8, A0
	ADD	$8, A1
	ADD	$-8, A2
	JMP	tail8

tail_bytes:
	BLE	A2, ZERO, barrier
	MOVBU	0(A1), A4
	MOVB	A4, 0(A0)
	ADD	$1, A0
	ADD	$1, A1
	ADD	$-1, A2
	JMP	tail_bytes

barrier:
	// FENCE rw,rw — full read-write ordering.
	// Ensures all preceding stores are visible to other harts before
	// any memory operation after this point.
	WORD	$0x0330000F		// FENCE rw,rw

done:
	RET
