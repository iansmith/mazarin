//go:build !test_stubs
// abi_stubs_arm64.s - ABI0 entry points for ksyscall functions
//
// TODO: This is a WORKAROUND for function pointer calls via the syscall table.
// The compiler generates ABI0 wrappers automatically, which is the approved way
// to handle cross-package and function pointer calls. We should investigate why
// our current usage doesn't work correctly with the compiler-generated wrappers
// and fix our code to be compatible with them, rather than using these tail-call
// stubs as a permanent solution.
//
// Background: When DispatchSyscall calls a handler via function pointer, Go uses
// ABI0 (stack-based calling convention). The compiler should handle the stack→register
// translation automatically, but we're seeing parameter corruption. This suggests
// we may be violating some assumption the ABI0 wrapper makes (stack alignment,
// nosplit chain, g pointer, etc.).
//
// See CLAUDE.md "Go ARM64 ABI - Assembly to Go Calls" for the tail-call pattern.

#include "textflag.h"

// SyscallFutex is called from dispatch.go via function pointer in syscallTable
// Go signature: func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64
// ABI0: 6 args (48 bytes) + 1 return (8 bytes) = 56 bytes
//
// Tail-call to the internal function. The .abi0 wrapper will read args from
// our caller's stack (which is exactly where they were placed by dispatch.go).
TEXT ·SyscallFutex(SB), NOSPLIT, $0-56
	JMP	·syscallFutexInternal(SB)
