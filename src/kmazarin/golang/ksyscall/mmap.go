//go:build qemuvirt && aarch64

package ksyscall

import (
	"sync/atomic"
	"unsafe"
)

// Kernel heap VA range comes from runtime configuration (auxv from Cardinal).
// These pages are NOT pre-mapped - they will be demand-paged on first access.
// NO hardcoded addresses.

// getRuntimeConfig is provided by main package via go:linkname.
func getRuntimeConfig() interface{}

// SyscallMmap implements the mmap(2) syscall
// Returns virtual addresses in the kernel heap range (high memory).
// Pages are NOT mapped immediately - they will be demand-paged on first access.
// The page fault handler will allocate a physical frame and map it.
//
//go:nosplit
func SyscallMmap(addr, length, prot, flags, fd, offset uint64) int64 {
	// Suppress unused warnings
	_ = prot
	_ = flags
	_ = fd
	_ = offset

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	var result uint64

	// Check if caller provided a hint address
	// Go's arena allocator expects to get exactly the address it requested
	// when allocating within an arena. We honor the hint if it's in heap range.
	cfg := getRuntimeConfigTyped()
	heapStart := uint64(cfg.KernelHeapStart)
	heapEnd := uint64(cfg.KernelHeapEnd)

	if addr != 0 && addr >= heapStart && addr+alignedLength <= heapEnd {
		// Honor the hint - this is critical for Go arena allocator
		result = addr
	} else {
		// No hint or hint outside heap range - use bump allocator
		result = bumpAlloc(alignedLength)
	}

	// Return result (or error)
	if result == 0 {
		return -12 // ENOMEM
	}
	return int64(result)
}

// Simple bump allocator for mmap
// Allocates from the kernel heap VA range (high memory, demand-paged)
// Thread-safe: uses atomic operations for concurrent allocation
var bumpPointer uint64 = 0
var bumpInitialized uint32 // uint32 for atomic operations

//go:nosplit
func bumpAlloc(size uint64) uint64 {
	// Lazy initialization from runtime config (atomic check)
	if atomic.LoadUint32(&bumpInitialized) == 0 {
		// Use compare-and-swap to ensure only one thread initializes
		if atomic.CompareAndSwapUint32(&bumpInitialized, 0, 1) {
			cfg := getRuntimeConfigTyped()
			atomic.StoreUint64(&bumpPointer, uint64(cfg.KernelHeapStart))
		} else {
			// Another thread won the race, wait for it to complete initialization
			for atomic.LoadUint32(&bumpInitialized) == 0 {
				// Spin wait
			}
		}
	}

	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Get heap end for bounds checking
	cfg := getRuntimeConfigTyped()
	heapEnd := uint64(cfg.KernelHeapEnd)

	// Atomically allocate from bump pointer
	for {
		currentPtr := atomic.LoadUint64(&bumpPointer)
		nextPtr := currentPtr + aligned

		// Check for wrap-around AND exceeding heap end
		if nextPtr < currentPtr || nextPtr > heapEnd {
			return 0 // Out of heap VA space or wrap-around
		}

		// Try to atomically update the bump pointer
		if atomic.CompareAndSwapUint64(&bumpPointer, currentPtr, nextPtr) {
			// Successfully allocated
			return currentPtr
		}
		// CAS failed, another thread allocated, retry
	}
}

// runtimeConfigStruct is a local copy of RuntimeConfig to avoid circular imports.
// Must match the layout in shared/constants/runtime_config.go exactly.
type runtimeConfigStruct struct {
	Magic                uint32
	Version              uint32
	DtbPhysAddr          uint64
	DtbSize              uint64
	DtbVirtAddr          uint64 // High-memory virtual address of DTB
	KmazarinPhysAddr     uint64
	KmazarinSize         uint64
	FramePoolStart       uint64
	FramePoolEnd         uint64
	KernelUartBase       uint64
	KernelGicBase        uint64
	TTBR1L0Phys          uint64
	TTBR0L0Phys          uint64
	StartupParamsAddr    uint64
	KernelVAOffset       uint64
	KernelPTPoolStart    uint64
	KernelPTPoolEnd      uint64
	KernelHeapStart      uint64
	KernelHeapEnd        uint64
	PageSize             uint64
	HWCap                uint64
	G0StackBottom        uint64
	G0StackTop           uint64
	G0StackSize          uint64 // Size of g0 stack
	ExceptionStackBottom uint64 // Bottom of exception stack
	ExceptionStackTop    uint64
	ExceptionStackSize   uint64
	G0StructAddr         uint64 // High-memory address where g0 struct should be copied
	AsyncPreemptAddr     uint64 // High-memory address of runtime.asyncPreempt function
}

// getRuntimeConfigTyped returns the runtime config with proper type.
// CRITICAL: Must extract data pointer from interface, not address of interface!
//
// Go interface layout: {tab *itab, data unsafe.Pointer}
// We need the data pointer (second field), not the interface itself.
//
//go:nosplit
func getRuntimeConfigTyped() *runtimeConfigStruct {
	cfgInterface := getRuntimeConfig()

	// Extract data pointer from interface (second field at offset 8)
	type iface struct {
		tab  *byte
		data unsafe.Pointer
	}
	ifacePtr := (*iface)(unsafe.Pointer(&cfgInterface))
	return (*runtimeConfigStruct)(ifacePtr.data)
}
