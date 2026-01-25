// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// DIPLOMAT OVERLAY: Stub out Windows syscalls for UEFI environment
//
// This file replaces runtime/syscall_windows.go with no-op stubs.
// All Windows syscalls are stubbed to eliminate kernel32.dll dependencies.

package runtime

import "unsafe"

// Errno type
type errno int32

const (
	_ERROR_OPERATION_ABORTED = 995
	_WAIT_OBJECT_0           = 0
	_WAIT_TIMEOUT            = 0x102
	_WAIT_FAILED             = 0xFFFFFFFF
)

// Windows types
type stdFunction unsafe.Pointer

// Stub implementations of Windows API functions
// These are normally loaded dynamically from kernel32.dll
// For UEFI, we provide no-op implementations

//go:nosplit
func stdcall0(fn stdFunction) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall1(fn stdFunction, a0 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall2(fn stdFunction, a0, a1 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall3(fn stdFunction, a0, a1, a2 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall4(fn stdFunction, a0, a1, a2, a3 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall5(fn stdFunction, a0, a1, a2, a3, a4 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall6(fn stdFunction, a0, a1, a2, a3, a4, a5 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall7(fn stdFunction, a0, a1, a2, a3, a4, a5, a6 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall8(fn stdFunction, a0, a1, a2, a3, a4, a5, a6, a7 uintptr) uintptr {
	throw("stdcall not available in UEFI")
	return 0
}

//go:nosplit
func stdcall(fn stdFunction) uintptr {
	return stdcall0(fn)
}

//go:nosplit
func syscall_loadsystemlibrary(name *uint16) (handle, err uintptr) {
	throw("LoadSystemLibrary not available in UEFI")
	return 0, 0
}

//go:nosplit
func syscall_loadlibrary(name *uint16) (handle, err uintptr) {
	throw("LoadLibrary not available in UEFI")
	return 0, 0
}

//go:nosplit
func syscall_getprocaddress(handle uintptr, name *byte) (proc uintptr, err uintptr) {
	throw("GetProcAddress not available in UEFI")
	return 0, 0
}

//go:nosplit
func syscall_Syscall(fn, nargs, a1, a2, a3 uintptr) (r1, r2, err uintptr) {
	throw("Syscall not available in UEFI")
	return 0, 0, 0
}

//go:nosplit
func syscall_Syscall6(fn, nargs, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr) {
	throw("Syscall6 not available in UEFI")
	return 0, 0, 0
}

//go:nosplit
func syscall_Syscall9(fn, nargs, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2, err uintptr) {
	throw("Syscall9 not available in UEFI")
	return 0, 0, 0
}

//go:nosplit
func syscall_Syscall12(fn, nargs, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12 uintptr) (r1, r2, err uintptr) {
	throw("Syscall12 not available in UEFI")
	return 0, 0, 0
}

//go:nosplit
func syscall_Syscall15(fn, nargs, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15 uintptr) (r1, r2, err uintptr) {
	throw("Syscall15 not available in UEFI")
	return 0, 0, 0
}

//go:nosplit
func syscall_Syscall18(fn, nargs, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17, a18 uintptr) (r1, r2, err uintptr) {
	throw("Syscall18 not available in UEFI")
	return 0, 0, 0
}

// Callback function types for Windows
type callbackFunc interface{}

//go:nosplit
func compileCallback(fn interface{}, cleanstack bool) uintptr {
	throw("callbacks not available in UEFI")
	return 0
}

//go:nosplit
func callbackasm()

//go:nosplit
func callbackasm1()

// Windows exception handling stubs
//go:nosplit
func exceptiontramp()

//go:nosplit
func firstcontinuetramp()

//go:nosplit
func lastcontinuetramp()

//go:nosplit
func initExceptionHandler() {
	// No exception handling in Phase 0
}
