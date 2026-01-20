
package ksyscall

import (
	"kmazarin/console"
	"sync/atomic"
	"unsafe"
)

// Userspace mmap allocates from low-memory range accessible via TTBR0.
// This is used by priest and other userspace programs.
//
// Range: 0x00400000 - 0x0000700000000000 (below the stack)
// Start at 4MB to leave room for ELF loading below that.
// End well below the userspace limit (bit 55) to leave room for stack.
// This gives ~112TB of VA space - plenty for Go runtime arenas.
//
// NOTE: MAP_FIXED and bump allocations may overlap. The page fault handler
// detects already-mapped pages and handles them correctly.
//
// Physical memory is only used when pages are actually faulted in.
const (
	userMmapStart = 0x00400000           // 4MB - above ELF load region
	userMmapEnd   = 0x0000700000000000   // 112TB - plenty of VA space
)

// userspaceActive is set to true when we jump to userspace.
// Mmap calls after this point (with addr=0) should use userspace allocator.
var userspaceActive uint32 // 0 = kernel only, 1 = userspace active

// SetUserspaceActive is called when we launch userspace to enable userspace mmap.
//
//go:nosplit
func SetUserspaceActive() {
	atomic.StoreUint32(&userspaceActive, 1)
}

// getRuntimeConfig is provided by main package via go:linkname.
func getRuntimeConfig() interface{}

// Linux mmap flags
const (
	_MAP_FIXED = 0x10
)

// SyscallMmap implements the mmap(2) syscall
// For userspace callers: returns virtual addresses in low memory (TTBR0).
// For kernel: uses kernel heap range (high memory, TTBR1).
// Pages are NOT mapped immediately - they will be demand-paged on first access.
//
//go:nosplit
func SyscallMmap(addr, length, prot, flags, fd, offset uint64) int64 {
	// Suppress unused warnings
	_ = prot
	_ = fd
	_ = offset

	// Zero-length mmap is invalid (EINVAL) - standard Linux behavior
	if length == 0 {
		return -22 // EINVAL
	}

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	var result uint64

	// Check if userspace is active and determine which allocator to use
	isUserspace := atomic.LoadUint32(&userspaceActive) != 0
	isMapFixed := (flags & _MAP_FIXED) != 0

	// Handle MAP_FIXED: must return the exact address or fail
	if isMapFixed && addr != 0 {
		console.KWriteString("[MMAP-FIX] addr=")
		console.KPrintHex64(addr)
		console.KWriteString(" len=")
		console.KPrintHex64(alignedLength)
		console.KWriteString("\r\n")
		if isUserspace && addr+alignedLength <= userMmapEnd {
			// Allow MAP_FIXED anywhere in userspace range.
			// If it overlaps with bump-allocated region, the page fault handler
			// will detect already-mapped pages and handle them correctly.
			// Pages will be demand-paged on access.
			result = addr
		} else if !isUserspace || addr >= 0x0000800000000000 {
			// Kernel fixed mapping
			cfg := getRuntimeConfigTyped()
			heapStart := uint64(cfg.KernelHeapStart)
			heapEnd := uint64(cfg.KernelHeapEnd)
			if addr >= heapStart && addr+alignedLength <= heapEnd {
				result = addr
			} else {
				return -12 // ENOMEM - can't honor MAP_FIXED at this address
			}
		} else {
			return -12 // ENOMEM - can't honor MAP_FIXED at this address
		}
	} else if isUserspace && (addr == 0 || addr < 0x0000800000000000) {
		// Userspace request without MAP_FIXED - use userspace allocator
		result = userBumpAlloc(alignedLength)
	} else if addr != 0 && addr >= 0x0000800000000000 {
		// Kernel hint in high memory - try to honor it
		cfg := getRuntimeConfigTyped()
		heapStart := uint64(cfg.KernelHeapStart)
		heapEnd := uint64(cfg.KernelHeapEnd)
		if addr >= heapStart && addr+alignedLength <= heapEnd {
			result = addr
		} else {
			result = bumpAlloc(alignedLength)
		}
	} else {
		// Kernel request (before userspace or high memory hint)
		result = bumpAlloc(alignedLength)
	}

	// Return result (or error)
	if result == 0 {
		return -12 // ENOMEM
	}
	return int64(result)
}

// Userspace bump allocator for mmap
// Allocates from low-memory range accessible via TTBR0
// Thread-safe: uses atomic operations for concurrent allocation
// Note: userBumpPointer is initialized directly to avoid race conditions
var userBumpPointer uint64 = userMmapStart

// GetUserMmapAllocEnd returns the current end of userspace mmap allocations.
// Any address >= this value has NOT been allocated by mmap.
// This is used by the page fault handler to validate fault addresses.
//
//go:nosplit
func GetUserMmapAllocEnd() uint64 {
	return atomic.LoadUint64(&userBumpPointer)
}

//go:nosplit
func userBumpAlloc(size uint64) uint64 {
	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Atomically allocate from bump pointer
	for {
		currentPtr := atomic.LoadUint64(&userBumpPointer)
		nextPtr := currentPtr + aligned

		// DEBUG: Print allocation attempt
		console.KWriteString("[BUMP] cur=")
		console.KPrintHex64(currentPtr)
		console.KWriteString(" size=")
		console.KPrintHex64(aligned)
		console.KWriteString(" next=")
		console.KPrintHex64(nextPtr)
		console.KWriteString(" end=")
		console.KPrintHex64(userMmapEnd)
		console.KWriteString("\r\n")

		// Check for wrap-around AND exceeding end
		if nextPtr < currentPtr || nextPtr > userMmapEnd {
			console.KWriteString("[BUMP] FAIL: ")
			if nextPtr < currentPtr {
				console.KWriteString("wrap-around")
			} else {
				console.KWriteString("exceeds end")
			}
			console.KWriteString("\r\n")
			return 0 // Out of VA space
		}

		if atomic.CompareAndSwapUint64(&userBumpPointer, currentPtr, nextPtr) {
			console.KWriteString("[BUMP] OK -> ")
			console.KPrintHex64(currentPtr)
			console.KWriteString("\r\n")
			return currentPtr
		}
	}
}

// Simple bump allocator for mmap (kernel)
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
