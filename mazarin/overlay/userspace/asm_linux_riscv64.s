// Copyright 2015 The Go Authors. All rights reserved.
// Copyright 2024 The Mazzy Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Mazzy overlay: assembly syscall implementation for RISC-V 64-bit Linux.
// This file replaces asm_linux_riscv64.s and adds defaultSyscallHandler.

#include "textflag.h"

// defaultSyscallHandler performs a real ECALL syscall.
// This is used as the default PriestSyscallEntry for programs that make
// real syscalls to the kernel (like shepherd itself).
//
// RISC-V Linux syscall ABI:
//   A7 (X17) = syscall number
//   A0-A5 (X10-X15) = arguments
//   ECALL
//   A0 (X10) = return value
//
// func defaultSyscallHandler(num, a1, a2, a3, a4, a5, a6 uintptr) int64
TEXT ·defaultSyscallHandler(SB),NOSPLIT,$0-64
	MOV	num+0(FP), A7	// syscall number
	MOV	a1+8(FP), A0	// arg1
	MOV	a2+16(FP), A1	// arg2
	MOV	a3+24(FP), A2	// arg3
	MOV	a4+32(FP), A3	// arg4
	MOV	a5+40(FP), A4	// arg5
	MOV	a6+48(FP), A5	// arg6
	ECALL
	MOV	A0, ret+56(FP)
	RET

// func rawVforkSyscall(trap, a1, a2, a3 uintptr) (r1, err uintptr)
TEXT ·rawVforkSyscall(SB),NOSPLIT,$0-48
	MOV	a1+8(FP), A0
	MOV	a2+16(FP), A1
	MOV	a3+24(FP), A2
	MOV	ZERO, A3
	MOV	ZERO, A4
	MOV	ZERO, A5
	MOV	trap+0(FP), A7	// syscall number
	ECALL
	MOV	$-4096, T0
	BLTU	T0, A0, err
	MOV	A0, r1+32(FP)	// r1
	MOV	ZERO, err+40(FP)	// errno
	RET
err:
	MOV	$-1, T0
	MOV	T0, r1+32(FP)	// r1
	NEG	A0, A0
	MOV	A0, err+40(FP)	// errno
	RET

// func rawSyscallNoError(trap uintptr, a1, a2, a3 uintptr) (r1, r2 uintptr);
TEXT ·rawSyscallNoError(SB),NOSPLIT,$0-48
	MOV	a1+8(FP), A0
	MOV	a2+16(FP), A1
	MOV	a3+24(FP), A2
	MOV	ZERO, A3
	MOV	ZERO, A4
	MOV	ZERO, A5
	MOV	trap+0(FP), A7	// syscall number
	ECALL
	MOV	A0, r1+32(FP)
	MOV	A1, r2+40(FP)
	RET
