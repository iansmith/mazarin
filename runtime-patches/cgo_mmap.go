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

//go:build (linux && (amd64 || arm64 || loong64)) || (freebsd && amd64)

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
	// Ultra-early debug: write call type to UART
	// 'A' = sysAllocOS (RW, no FIXED) - allocate ready memory
	// 'R' = sysReserveOS (NONE, no FIXED) - reserve address space
	// 'M' = sysMapOS (RW, FIXED) - map reserved space
	const uartBase = uintptr(0xFFFFFFFF09000000)
	isFixed := (flags & _kmazarinMapFixed) != 0
	isProtNone := (prot == 0) // PROT_NONE

	if isFixed {
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('M') // sysMapOS
	} else if isProtNone {
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('R') // sysReserveOS
	} else {
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('A') // sysAllocOS
	}

	_ = fd
	_ = off

	// Zero-length mmap is invalid - but print more debug info first
	if n == 0 {
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('Z') // Debug: length=0
		// Print whether it had MAP_FIXED (if 'M' was printed above, this confirms)
		if isFixed {
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('!')
		}
		// Debug: print address as hex
		addrVal := uint64(uintptr(addr))
		hexChars := "0123456789ABCDEF"
		for i := 60; i >= 0; i -= 4 {
			nibble := (addrVal >> i) & 0xF
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(hexChars[nibble])
		}
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('\r')
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('\n')
		return nil, 22 // EINVAL
	}

	// Get heap bounds from auxv-derived variables (set by archauxv before mallocinit)
	heapStart := uint64(kmazarinHeapStart)
	heapEnd := uint64(kmazarinHeapEnd)

	// Debug: print heap bounds on first successful mmap
	if kmazarinBumpInitialized.Load() == 0 {
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('H')
		// Print heapStart
		hexChars := "0123456789ABCDEF"
		for i := 60; i >= 0; i -= 4 {
			nibble := (heapStart >> i) & 0xF
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(hexChars[nibble])
		}
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('-')
		// Print heapEnd
		for i := 60; i >= 0; i -= 4 {
			nibble := (heapEnd >> i) & 0xF
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(hexChars[nibble])
		}
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('\r')
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('\n')
	}

	// Sanity check: if auxv wasn't parsed, heap bounds will be 0
	if heapStart == 0 || heapEnd == 0 {
		// This should never happen - archauxv runs before mallocinit
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('E') // Error: heap bounds not set
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
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('N') // Null address with MAP_FIXED
			return nil, 22 // EINVAL - MAP_FIXED with null address
		}
		addrVal := uint64(uintptr(addr))
		// Debug: print address bits 47:32
		hexChars := "0123456789ABCDEF"
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(':')
		for i := 44; i >= 32; i -= 4 {
			nibble := (addrVal >> i) & 0xF
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(hexChars[nibble])
		}
		// For MAP_FIXED, just return the requested address
		// The page fault handler will allocate physical pages on demand
		if addrVal >= heapStart && addrVal+alignedLength <= heapEnd {
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('+') // Success
			return addr, 0
		}
		*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('X') // Out of range
		return nil, 12 // ENOMEM
	}

	// Non-MAP_FIXED: bump allocate from heap
	// Use a simple CAS loop
	for {
		// Load current pointer value
		currentPtr := kmazarinBumpPointer.Load()
		nextPtr := currentPtr + alignedLength

		// Check bounds
		if nextPtr < currentPtr || nextPtr > heapEnd {
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('!') // Out of bounds
			return nil, 12 // ENOMEM
		}

		// Try to atomically update the bump pointer
		if kmazarinBumpPointer.CompareAndSwap(currentPtr, nextPtr) {
			// Debug: print allocated address (just high nibbles for brevity)
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(':')
			hexChars := "0123456789ABCDEF"
			// Just print bytes 6-4 (bits 47:32) to show the unique part
			for i := 44; i >= 32; i -= 4 {
				nibble := (currentPtr >> i) & 0xF
				*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32(hexChars[nibble])
			}
			*(*uint32)(noescape(unsafe.Pointer(uartBase))) = uint32('+') // Success
			return unsafe.Pointer(uintptr(currentPtr)), 0
		}
		// CAS failed, retry
	}
}

// munmap is a no-op during early boot - we don't actually free memory.
//
//go:nosplit
func munmap(addr unsafe.Pointer, n uintptr) {
	// No-op: don't free memory during early boot
	_ = addr
	_ = n
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
