// Copyright 2015 The Go Authors. All rights reserved.
// Copyright 2024 The Mazzy Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Mazzy overlay: assembly syscall implementation for arm64 Linux.
// This file replaces asm_linux_arm64.s and adds defaultSyscallHandler.

#include "textflag.h"

// defaultSyscallHandler performs a real SVC syscall.
// This is used as the default PriestSyscallEntry for programs that make
// real syscalls to the kernel (like shepherd itself).
//
// func defaultSyscallHandler(num, a1, a2, a3, a4, a5, a6 uintptr) int64
TEXT ·defaultSyscallHandler(SB),NOSPLIT,$0-64
	MOVD	num+0(FP), R8	// syscall number
	MOVD	a1+8(FP), R0	// arg1
	MOVD	a2+16(FP), R1	// arg2
	MOVD	a3+24(FP), R2	// arg3
	MOVD	a4+32(FP), R3	// arg4
	MOVD	a5+40(FP), R4	// arg5
	MOVD	a6+48(FP), R5	// arg6
	SVC
	MOVD	R0, ret+56(FP)
	RET

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT,$0-48
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	MOVD	$0, R3
	MOVD	$0, R4
	MOVD	$0, R5
	MOVD	trap+0(FP), R8	// syscall entry
	SVC
	CMN	$4095, R0
	BCC	ok
	MOVD	$-1, R4
	MOVD	R4, r1+32(FP)	// r1
	NEG	R0, R0
	MOVD	R0, err+40(FP)	// errno
	RET
ok:
	MOVD	R0, r1+32(FP)	// r1
	MOVD	ZR, err+40(FP)	// errno
	RET

// func rawSyscallNoError(trap uintptr, a1, a2, a3 uintptr) (r1, r2 uintptr);
TEXT ·rawSyscallNoError(SB),NOSPLIT,$0-48
	MOVD	a1+8(FP), R0
	MOVD	a2+16(FP), R1
	MOVD	a3+24(FP), R2
	MOVD	$0, R3
	MOVD	$0, R4
	MOVD	$0, R5
	MOVD	trap+0(FP), R8	// syscall entry
	SVC
	MOVD	R0, r1+32(FP)
	MOVD	R1, r2+40(FP)
	RET
