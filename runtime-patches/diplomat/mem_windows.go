// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// DIPLOMAT OVERLAY: Stub out Windows memory management for UEFI
//
// This file replaces runtime/mem_windows.go with UEFI-compatible stubs.
// For Phase 0, we provide minimal implementations that defer actual
// memory allocation to UEFI Boot Services (to be implemented in Phase 1).

package runtime

import (
	"unsafe"
)

const (
	_MEM_COMMIT     = 0x1000
	_MEM_RESERVE    = 0x2000
	_MEM_DECOMMIT   = 0x4000
	_MEM_RELEASE    = 0x8000
	_PAGE_READWRITE = 0x04
	_PAGE_NOACCESS  = 0x01
)

// sysAllocOS allocates memory from the operating system.
// For UEFI Phase 0, we throw an error.
// Phase 1 will implement this using UEFI AllocatePages.
//
//go:nosplit
func sysAllocOS(n uintptr) unsafe.Pointer {
	throw("sysAllocOS: UEFI memory allocation not yet implemented (Phase 0)")
	return nil
}

// sysUnusedOS tells the OS that memory doesn't need to be backed by physical memory.
//
//go:nosplit
func sysUnusedOS(v unsafe.Pointer, n uintptr) {
	// No-op for Phase 0
	// UEFI doesn't have an equivalent of VirtualFree(MEM_DECOMMIT)
}

// sysUsedOS tells the OS that memory will be used.
//
//go:nosplit
func sysUsedOS(v unsafe.Pointer, n uintptr) {
	// No-op for Phase 0
	// UEFI memory is always "committed" once allocated
}

// sysFreeOS frees memory back to the OS.
//
//go:nosplit
func sysFreeOS(v unsafe.Pointer, n uintptr) {
	// No-op for Phase 0
	// Phase 1 will implement using UEFI FreePages
}

// sysReserveOS reserves address space without allocating memory.
//
//go:nosplit
func sysReserveOS(v unsafe.Pointer, n uintptr) unsafe.Pointer {
	// For Phase 0, just return the requested address
	// UEFI doesn't have a "reserve without allocating" concept like Windows
	// Phase 1 will need to track this carefully
	return v
}

// sysMapOS maps previously reserved memory.
//
//go:nosplit
func sysMapOS(v unsafe.Pointer, n uintptr) {
	// No-op for Phase 0
	// In UEFI, we'll allocate on-demand in Phase 1
}

// Windows exception handling hooks
// These are referenced by the runtime but not used in UEFI
const callbackMaxFrame = 0

type callbackArgs struct {
	index uintptr
	// args points to the argument block.
	args uintptr
	// below points to the return value.
	// On x86, the return value is the single word at the very bottom of the stack.
	below uintptr
}

func callbackWrap(a *callbackArgs) uintptr {
	throw("callbacks not supported in UEFI")
	return 0
}

//go:nosplit
func osRelax(relax bool) uint32 {
	// No-op for UEFI
	return 0
}
