// Copyright 2015 The Go Authors. All rights reserved.
// Copyright 2024 The Mazzy Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Mazzy overlay: assembly syscall implementation for AMD64 Linux.
// This file replaces asm_linux_amd64.s and adds defaultSyscallHandler.

#include "textflag.h"

// defaultSyscallHandler performs a real SYSCALL.
// This is used as the default PriestSyscallEntry for programs that make
// real syscalls to the kernel (like priest itself).
//
// func defaultSyscallHandler(num, a1, a2, a3, a4, a5, a6 uintptr) int64
TEXT ·defaultSyscallHandler(SB),NOSPLIT,$0-64
	MOVQ	num+0(FP), AX	// syscall number
	MOVQ	a1+8(FP), DI	// arg1
	MOVQ	a2+16(FP), SI	// arg2
	MOVQ	a3+24(FP), DX	// arg3
	MOVQ	a4+32(FP), R10	// arg4 (R10 not RCX due to SYSCALL clobbering RCX)
	MOVQ	a5+40(FP), R8	// arg5
	MOVQ	a6+48(FP), R9	// arg6
	SYSCALL
	MOVQ	AX, ret+56(FP)
	RET

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT,$0-48
	MOVQ	trap+0(FP), AX	// syscall number
	MOVQ	a1+8(FP), DI
	MOVQ	a2+16(FP), SI
	MOVQ	a3+24(FP), DX
	MOVQ	$0, R10
	MOVQ	$0, R8
	MOVQ	$0, R9
	SYSCALL
	CMPQ	AX, $0xfffffffffffff001	// -4095
	JLS	ok
	MOVQ	$-1, BX
	MOVQ	BX, r1+32(FP)	// r1
	NEGQ	AX
	MOVQ	AX, err+40(FP)	// errno
	RET
ok:
	MOVQ	AX, r1+32(FP)	// r1
	MOVQ	$0, err+40(FP)	// errno
	RET

// func rawSyscallNoError(trap uintptr, a1, a2, a3 uintptr) (r1, r2 uintptr);
TEXT ·rawSyscallNoError(SB),NOSPLIT,$0-48
	MOVQ	trap+0(FP), AX	// syscall number
	MOVQ	a1+8(FP), DI
	MOVQ	a2+16(FP), SI
	MOVQ	a3+24(FP), DX
	MOVQ	$0, R10
	MOVQ	$0, R8
	MOVQ	$0, R9
	SYSCALL
	MOVQ	AX, r1+32(FP)
	MOVQ	DX, r2+40(FP)
	RET
