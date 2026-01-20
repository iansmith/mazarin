// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// KMAZARIN OVERLAY: Implements mmap/munmap directly as bump allocation.
// This avoids SVC instructions which would trap to exception context.
// Uses a simple bump allocator that requires NO external package dependencies.
//
// CRITICAL: This runs during Go runtime init, BEFORE any package init() functions.
// We use only primitive operations available in the runtime package itself.

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

// Early-boot heap allocator constants (must match layout.go)
const (
	kmazarinHeapStart = 0xFFFF000100000000 // KernelHeapStart from layout.go
	kmazarinHeapEnd   = 0xFFFF100000000000 // KernelHeapEnd from layout.go
	kmazarinPageSize  = 4096
	kmazarinMapFixed  = 0x10 // MAP_FIXED flag
)

// kmazarinBumpPointer is the early-boot allocator for mmap calls.
// Initialized directly in declaration - NO init() function needed.
var kmazarinBumpPointer atomic.Uint64

// kmazarinBumpInitialized tracks whether we've set the initial value.
// We can't use init() because this runs before init() during runtime bootstrap.
var kmazarinBumpInitialized atomic.Uint32

// mmap implements the mmap system call using a simple bump allocator.
// This is called during early Go runtime init before ANY package init() runs.
// Uses only primitive operations that work without full runtime.
//
//go:nosplit
func mmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uint32) (unsafe.Pointer, int) {
	_ = prot
	_ = fd
	_ = off

	// Zero-length mmap is invalid
	if n == 0 {
		return nil, 22 // EINVAL
	}

	// Lazy initialize bump pointer (can't use init() during early runtime)
	if kmazarinBumpInitialized.Load() == 0 {
		if kmazarinBumpInitialized.CompareAndSwap(0, 1) {
			kmazarinBumpPointer.Store(kmazarinHeapStart)
		}
	}

	// Align length to page size
	alignedLength := (uint64(n) + kmazarinPageSize - 1) & ^uint64(kmazarinPageSize-1)

	// Handle MAP_FIXED: must return the exact address
	if (flags & kmazarinMapFixed) != 0 {
		if addr == nil {
			return nil, 22 // EINVAL - MAP_FIXED with null address
		}
		addrVal := uint64(uintptr(addr))
		// For MAP_FIXED, just return the requested address
		// The page fault handler will allocate physical pages on demand
		if addrVal >= kmazarinHeapStart && addrVal+alignedLength <= kmazarinHeapEnd {
			return addr, 0
		}
		return nil, 12 // ENOMEM
	}

	// Non-MAP_FIXED: bump allocate from heap
	// Use a simple CAS loop
	for {
		// Load current pointer value
		currentPtr := kmazarinBumpPointer.Load()
		nextPtr := currentPtr + alignedLength

		// Check bounds
		if nextPtr < currentPtr || nextPtr > kmazarinHeapEnd {
			return nil, 12 // ENOMEM
		}

		// Try to atomically update the bump pointer
		if kmazarinBumpPointer.CompareAndSwap(currentPtr, nextPtr) {
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
