
package ksyscall

import (
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

// Userspace mmap allocates from low-memory range accessible via TTBR0 (or equivalent).
// This is used by priest and other userspace programs.
//
// Per-process bump pointers and span groups live in proc.Priest (proc package),
// accessed via proc.CurrentPriest(). Physical memory is only used when pages
// are actually faulted in.
const (
	userMmapEnd = 0x0000700000000000 // 112TB - plenty of VA space
)

// Userspace framebuffer mapping constants.
// The framebuffer is mapped at a fixed VA for all priests to allow UI rendering.
// Located below the stack region to avoid conflicts.
const (
	UserFramebufferVA   = 0x00007FFE00000000 // Fixed VA for framebuffer in priest space
	UserFramebufferSize = 0x2000000          // 32MB - matches FramebufferSize in shared/constants
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

// Linux mmap flags
const (
	_MAP_FIXED = 0x10
)

// SyscallMmap implements the mmap(2) syscall
// For userspace callers: returns virtual addresses in low memory (TTBR0).
// For kernel: uses kernel heap range (high memory, TTBR1).
// Pages are NOT mapped immediately - they will be demand-paged on first access.
//
// Hint handling (addr != 0 without MAP_FIXED):
// - If the hint is valid and doesn't overlap existing spans, honor it
// - Otherwise fall back to bump allocator
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
	// MAP_FIXED can overwrite existing mappings (dangerous but that's the semantics)
	if isMapFixed && addr != 0 {
		if isUserspace && addr >= userMmapStart && addr+alignedLength <= userMmapEnd {
			// Remove any existing spans that overlap, then add new span
			removeSpan(addr, alignedLength)
			if !addSpan(addr, alignedLength) {
				return -12 // ENOMEM
			}
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
	} else if isUserspace && addr < 0x0000800000000000 {
		// Userspace request without MAP_FIXED
		if addr != 0 && addr >= userMmapStart && addr+alignedLength <= userMmapEnd {
			// Try to honor the hint if it doesn't overlap existing spans
			result = tryReserveHint(addr, alignedLength)
			if result == 0 {
				// Hint couldn't be honored, fall back to bump allocator
				result = userBumpAlloc(alignedLength)
				if result != 0 {
					addSpan(result, alignedLength)
				}
			}
		} else {
			// No hint or invalid hint - use bump allocator
			result = userBumpAlloc(alignedLength)
			if result != 0 {
				addSpan(result, alignedLength)
			}
		}
	} else if addr != 0 && addr >= 0x0000800000000000 {
		// Kernel hint in high memory - try to honor it
		cfg := getRuntimeConfigTyped()
		heapStart := uint64(cfg.KernelHeapStart)
		heapEnd := uint64(cfg.KernelHeapEnd)
		if addr >= heapStart && addr+alignedLength <= heapEnd {
			result = addr
		} else {
			result = kernelBumpAlloc(alignedLength)
		}
	} else {
		// Kernel request (before userspace or high memory hint)
		result = kernelBumpAlloc(alignedLength)
	}

	// Return result (or error)
	if result == 0 {
		return -12 // ENOMEM
	}

	return int64(result)
}


// GetUserMmapAllocEnd returns the current end of userspace mmap allocations
// for the calling process. Any address >= this value has NOT been allocated.
// This is used by the page fault handler to validate fault addresses.
//
//go:nosplit
func GetUserMmapAllocEnd() uint64 {
	p := proc.CurrentPriest()
	if p == nil {
		return userMmapStart
	}
	v := atomic.LoadUint64(&p.BumpPointer)
	if v == 0 {
		return userMmapStart
	}
	return v
}

//go:nosplit
func userBumpAlloc(size uint64) uint64 {
	p := proc.CurrentPriest()
	if p == nil {
		return 0 // No priest — kernel context, don't bump-alloc userspace VA
	}

	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Atomically allocate from this priest's bump pointer
	for {
		currentPtr := atomic.LoadUint64(&p.BumpPointer)

		// Lazy initialization: first allocation starts at userMmapStart
		if currentPtr == 0 {
			atomic.CompareAndSwapUint64(&p.BumpPointer, 0, userMmapStart)
			continue
		}

		nextPtr := currentPtr + aligned

		// Check if this allocation would overlap any existing span
		if findSpanOverlapEnd(currentPtr, aligned) != 0 {
			return 0 // ENOMEM - allocation conflicts with reserved span
		}

		// Check for wrap-around AND exceeding end
		if nextPtr < currentPtr || nextPtr > userMmapEnd {
			return 0 // Out of VA space
		}

		if atomic.CompareAndSwapUint64(&p.BumpPointer, currentPtr, nextPtr) {
			return currentPtr
		}
	}
}

// bumpAllocForPriest allocates VA space from a specific priest's bump pointer.
// Same logic as userBumpAlloc but operates on an explicit priest, not CurrentPriest().
// Used by IPC syscalls that need to allocate VA in a target process.
//
//go:nosplit
func bumpAllocForPriest(p *proc.Priest, size uint64) uint64 {
	if p == nil {
		return 0
	}

	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	for {
		currentPtr := atomic.LoadUint64(&p.BumpPointer)

		// Lazy initialization: first allocation starts at userMmapStart
		if currentPtr == 0 {
			atomic.CompareAndSwapUint64(&p.BumpPointer, 0, userMmapStart)
			continue
		}

		nextPtr := currentPtr + aligned

		// Check if this allocation would overlap any existing span
		if p.Spans.FindOverlapEnd(currentPtr, aligned) != 0 {
			return 0 // ENOMEM - allocation conflicts with reserved span
		}

		// Check for wrap-around AND exceeding end
		if nextPtr < currentPtr || nextPtr > userMmapEnd {
			return 0 // Out of VA space
		}

		if atomic.CompareAndSwapUint64(&p.BumpPointer, currentPtr, nextPtr) {
			return currentPtr
		}
	}
}

// ============================================================================
// Kernel bump allocator (for kmazarin's own heap, high memory via TTBR1)
// ============================================================================
// This is separate from userspace allocation. The kernel has a single global
// bump allocator since kmazarin is a single "process".

var kernelBumpPointer uint64 = 0
var kernelBumpInitialized uint32 // uint32 for atomic operations

//go:nosplit
func kernelBumpAlloc(size uint64) uint64 {
	// Lazy initialization from runtime config (atomic check)
	if atomic.LoadUint32(&kernelBumpInitialized) == 0 {
		// Use compare-and-swap to ensure only one thread initializes
		if atomic.CompareAndSwapUint32(&kernelBumpInitialized, 0, 1) {
			cfg := getRuntimeConfigTyped()
			atomic.StoreUint64(&kernelBumpPointer, uint64(cfg.KernelHeapStart))
		} else {
			// Another thread won the race, wait for it to complete initialization
			for atomic.LoadUint32(&kernelBumpInitialized) == 0 {
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
		currentPtr := atomic.LoadUint64(&kernelBumpPointer)
		nextPtr := currentPtr + aligned

		// Check for wrap-around AND exceeding heap end
		if nextPtr < currentPtr || nextPtr > heapEnd {
			return 0 // Out of heap VA space or wrap-around
		}

		// Try to atomically update the bump pointer
		if atomic.CompareAndSwapUint64(&kernelBumpPointer, currentPtr, nextPtr) {
			// Successfully allocated
			return currentPtr
		}
		// CAS failed, another thread allocated, retry
	}
}

// runtimeConfigStruct is the full config view returned by main package.
// Layout MUST match main.fullConfig exactly.
type runtimeConfigStruct struct {
	KernelVAOffset          uint64
	KmazarinSize            uint64
	KmazarinPhysAddr        uint64
	FramePoolStart          uint64
	FramePoolEnd            uint64
	KernelPTPoolStart       uint64
	KernelPTPoolEnd         uint64
	KernelHeapStart         uint64
	KernelHeapEnd           uint64
	G0StackBottom           uint64
	G0StackTop              uint64
	G0StackSize             uint64
	ExceptionStackBottom    uint64
	ExceptionStackTop       uint64
	ExceptionStackSize      uint64
	FramebufferPhysAddr     uint64
	FramebufferSize         uint64
	BootImagePhysAddr       uint64
	BootImageSize           uint64
	TotalRAMSize            uint64
	RAMBaseAddr             uint64
	UserspaceFramePoolStart uint64
	UserspaceFramePoolEnd   uint64
	UserspacePTPoolStart    uint64
	UserspacePTPoolEnd      uint64
	DtbPhysAddr             uint64
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
