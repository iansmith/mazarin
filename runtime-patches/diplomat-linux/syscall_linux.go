// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Diplomat runtime overlay for syscall routing.
// Routes all syscalls through a central dispatcher in diplomat/main.

package syscall

import _ "unsafe"

//go:linkname diplomatSyscallDispatch main.DiplomatSyscallDispatch
func diplomatSyscallDispatch(num, a1, a2, a3, a4, a5, a6 uintptr) int64

//go:uintptrkeepalive
//go:nosplit
//go:norace
//go:linkname RawSyscall6
func RawSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	result := diplomatSyscallDispatch(trap, a1, a2, a3, a4, a5, a6)
	if result < 0 {
		return 0, 0, Errno(-result)
	}
	return uintptr(result), 0, 0
}

//go:uintptrkeepalive
//go:nosplit
//go:linkname Syscall6
func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	entersyscall()
	result := diplomatSyscallDispatch(trap, a1, a2, a3, a4, a5, a6)
	exitsyscall()
	if result < 0 {
		return 0, 0, Errno(-result)
	}
	return uintptr(result), 0, 0
}

//go:uintptrkeepalive
//go:nosplit
//go:linkname Syscall
func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	entersyscall()
	result := diplomatSyscallDispatch(trap, a1, a2, a3, 0, 0, 0)
	exitsyscall()
	if result < 0 {
		return 0, 0, Errno(-result)
	}
	return uintptr(result), 0, 0
}

//go:uintptrkeepalive
//go:nosplit
//go:norace
//go:linkname RawSyscall
func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	result := diplomatSyscallDispatch(trap, a1, a2, a3, 0, 0, 0)
	if result < 0 {
		return 0, 0, Errno(-result)
	}
	return uintptr(result), 0, 0
}

// syscall_rawSyscallNoError is for runtime use only
//go:linkname syscall_rawSyscallNoError
func syscall_rawSyscallNoError(trap, a1, a2, a3 uintptr) (r1, r2 uintptr) {
	result := diplomatSyscallDispatch(trap, a1, a2, a3, 0, 0, 0)
	if result < 0 {
		return 0, 0
	}
	return uintptr(result), 0
}

// syscall_syscallN is for runtime use only (variadic syscalls)
//go:linkname syscall_syscallN
func syscall_syscallN(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1 uintptr, err Errno) {
	entersyscall()
	result := diplomatSyscallDispatch(trap, a1, a2, a3, a4, a5, a6)
	exitsyscall()
	if result < 0 {
		return 0, Errno(-result)
	}
	return uintptr(result), 0
}
