//go:build !test_stubs

#include "textflag.h"

// SyscallFutex is called from dispatch.go via function pointer in syscallTable.
// Go signature: func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64
// ABI0: 6 args (48 bytes) + 1 return (8 bytes) = 56 bytes
TEXT ·SyscallFutex(SB), NOSPLIT, $0-56
	JMP	·syscallFutexInternal(SB)
