//go:build !test_stubs

#include "textflag.h"

// SyscallFutex is called from dispatch.go via function pointer in syscallTable.
// Go signature: func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64
// ABI0: 6 args (48 bytes) + 1 return (8 bytes) = 56 bytes
TEXT ·SyscallFutex(SB), NOSPLIT, $0-56
	JMP	·syscallFutexInternal(SB)

// writeFS sets the FS_BASE MSR to the given address.
// Used by arch_prctl(ARCH_SET_FS) for userspace TLS setup.
// func writeFS(addr uint64)
TEXT ·writeFS(SB), NOSPLIT, $0-8
	MOVQ	addr+0(FP), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR
	RET
