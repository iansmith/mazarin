// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// KMAZARIN OVERLAY: Implements mmap/munmap directly as bump allocation.
// This avoids SVC instructions which would trap to exception context.
// Uses a simple bump allocator that requires NO external package dependencies.
//
// CRITICAL: This runs during Go runtime init, BEFORE any package init() functions.
// We use only primitive operations available in the runtime package itself.
//
// Heap bounds are passed via custom auxv entries (AT_KMAZARIN_HEAP_START/END)
// and parsed by archauxv() in os_linux_arm64.go BEFORE this code runs.

//go:build (linux && (amd64 || arm64 || loong64 || riscv64)) || (freebsd && amd64)

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// _cgo_mmap is filled in by runtime/cgo when it is linked into the
// program, so it is only non-nil when using cgo.
//
//go:linkname _cgo_mmap _cgo_mmap
var _cgo_mmap unsafe.Pointer

// _cgo_munmap is filled in by runtime/cgo when it is linked into the
// program, so it is only non-nil when using cgo.
//
//go:linkname _cgo_munmap _cgo_munmap
var _cgo_munmap unsafe.Pointer

// Heap allocator constants
const (
	_kmazarinPageSize = 4096
	_kmazarinMapFixed = 0x10 // MAP_FIXED flag
	_kmazarinProtNone = 0x0  // PROT_NONE
)

// kmazarinBumpPointer is the early-boot allocator for mmap calls.
// Initialized lazily from auxv-derived kmazarinHeapStart.
var kmazarinBumpPointer atomic.Uint64

// kmazarinBumpInitialized tracks whether we've set the initial value.
// We can't use init() because this runs before init() during runtime bootstrap.
var kmazarinBumpInitialized atomic.Uint32

// mmap implements the mmap system call using a simple bump allocator.
// This is called during early Go runtime init before ANY package init() runs.
// Uses only primitive operations that work without full runtime.
//
// Heap bounds come from kmazarinHeapStart/End variables in os_linux_arm64.go,
// which are set by archauxv() from custom auxv entries before this runs.
//
//go:nosplit
func mmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (unsafe.Pointer, int) {
	isFixed := (flags & _kmazarinMapFixed) != 0

	_ = fd
	_ = off

	// Zero-length mmap is invalid
	if n == 0 {
		return nil, 22 // EINVAL
	}

	// Get heap bounds from auxv-derived variables (set by archauxv before mallocinit)
	heapStart := uint64(kmazarinHeapStart)
	heapEnd := uint64(kmazarinHeapEnd)

	// Sanity check: if auxv wasn't parsed, heap bounds will be 0
	if heapStart == 0 || heapEnd == 0 {
		*(*byte)(unsafe.Pointer(uintptr(0xFFFFFFFF09000000))) = '!'
		return nil, 12 // ENOMEM
	}

	// Lazy initialize bump pointer from auxv-derived heap start
	if kmazarinBumpInitialized.Load() == 0 {
		if kmazarinBumpInitialized.CompareAndSwap(0, 1) {
			kmazarinBumpPointer.Store(heapStart)
		}
	}

	// Align length to page size
	alignedLength := (uint64(n) + _kmazarinPageSize - 1) & ^uint64(_kmazarinPageSize-1)

	// Handle MAP_FIXED: must return the exact address
	if isFixed {
		if addr == nil {
			return nil, 22 // EINVAL - MAP_FIXED with null address
		}
		addrVal := uint64(uintptr(addr))
		// For MAP_FIXED, just return the requested address
		// The page fault handler will allocate physical pages on demand
		if addrVal >= heapStart && addrVal+alignedLength <= heapEnd {
			return addr, 0
		}
		return nil, 12 // ENOMEM
	}

	// PROT_NONE with a hint address: this is sysReserveOS asking to reserve VA
	// space. Honor the hint if it falls within our heap pool — no physical
	// backing is needed, the page fault handler allocates frames on demand
	// when sysMapOS later issues MAP_FIXED within this range.
	// We don't advance the bump pointer because this is just a VA reservation.
	if prot == _kmazarinProtNone && addr != nil {
		hintVal := uint64(uintptr(addr))
		if hintVal >= heapStart && hintVal+alignedLength <= heapEnd {
			return addr, 0
		}
		return nil, 12 // ENOMEM — hint not in our pool
	}

	// Non-MAP_FIXED: bump allocate from heap
	// Use a simple CAS loop
	for {
		// Load current pointer value
		currentPtr := kmazarinBumpPointer.Load()
		nextPtr := currentPtr + alignedLength

		// Check bounds
		if nextPtr < currentPtr || nextPtr > heapEnd {
			return nil, 12 // ENOMEM
		}

		// Try to atomically update the bump pointer
		if kmazarinBumpPointer.CompareAndSwap(currentPtr, nextPtr) {
			return unsafe.Pointer(uintptr(currentPtr)), 0
		}
		// CAS failed, retry
	}
}

// kmazarinPrintHex40 prints the low 40 bits of a uint64 as 10 hex digits via UART.
// Covers addresses up to 1TB which is sufficient for all heap regions.
//
//go:nosplit
func kmazarinPrintHex40(v uint64) {
	for i := uint(36); ; i -= 4 {
		nibble := byte((v >> i) & 0xF)
		if nibble < 10 {
			kmazarinUART('0' + nibble)
		} else {
			kmazarinUART('A' + nibble - 10)
		}
		if i == 0 {
			break
		}
	}
}

// kmazarinPrintHex32 prints a uint32 as 8 hex digits via UART.
//
//go:nosplit
func kmazarinPrintHex32(v uint32) {
	for i := uint(28); ; i -= 4 {
		nibble := byte((v >> i) & 0xF)
		if nibble < 10 {
			kmazarinUART('0' + nibble)
		} else {
			kmazarinUART('A' + nibble - 10)
		}
		if i == 0 {
			break
		}
	}
}

// munmap is a stub — we don't actually free memory yet.
//
//go:nosplit
func munmap(addr unsafe.Pointer, n uintptr) {
}

// sysMmap is declared but never called in kmazarin.
// We keep it to satisfy any references in the runtime.
func sysMmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (p unsafe.Pointer, err int)

// callCgoMmap is declared but never called in kmazarin.
func callCgoMmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) uintptr

// sysMunmap is declared but never called in kmazarin.
func sysMunmap(addr unsafe.Pointer, n uintptr)

// callCgoMunmap is declared but never called in kmazarin.
func callCgoMunmap(addr unsafe.Pointer, n uintptr)
