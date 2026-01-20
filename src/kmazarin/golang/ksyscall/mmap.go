
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
// Physical memory is only used when pages are actually faulted in.
const (
	userMmapStart = 0x00400000         // 4MB - above ELF load region
	userMmapEnd   = 0x0000700000000000 // 112TB - plenty of VA space
)

// Userspace framebuffer mapping constants.
// The framebuffer is mapped at a fixed VA for all priests to allow UI rendering.
// Located below the stack region to avoid conflicts.
const (
	UserFramebufferVA   = 0x00007FFE00000000 // Fixed VA for framebuffer in priest space
	UserFramebufferSize = 0x2000000          // 32MB - matches FramebufferSize in shared/constants
)

// ============================================================================
// Span tracking wrapper functions
// ============================================================================
// These functions delegate to the current process's SpanGroup.
// When per-process tracking is implemented, GetCurrentSpanGroup() will
// return the appropriate SpanGroup for the calling process.

// addSpan records a new VA reservation for the current process.
//
//go:nosplit
func addSpan(start, length uint64) bool {
	return GetCurrentSpanGroup().Add(start, length)
}

// removeSpan removes/splits spans overlapping the given range.
//
//go:nosplit
func removeSpan(start, length uint64) {
	GetCurrentSpanGroup().Remove(start, length)
}

// IsAddressInSpan checks if an address falls within any reserved span.
// Used by the page fault handler to validate hint-based allocations.
//
//go:nosplit
func IsAddressInSpan(addr uint64) bool {
	return GetCurrentSpanGroup().Contains(addr)
}

// tryReserveHint attempts to reserve a hint address if it doesn't overlap.
//
//go:nosplit
func tryReserveHint(hint, length uint64) uint64 {
	return GetCurrentSpanGroup().TryReserve(hint, length)
}

// findSpanOverlapEnd returns the end of any span overlapping [start, start+length).
// Returns 0 if no overlap. Used by bump allocator to skip past reserved regions.
//
//go:nosplit
func findSpanOverlapEnd(start, length uint64) uint64 {
	return GetCurrentSpanGroup().FindOverlapEnd(start, length)
}

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
// Hint handling (addr != 0 without MAP_FIXED):
// - If the hint is valid and doesn't overlap existing spans, honor it
// - Otherwise fall back to bump allocator
//
//go:nosplit
func SyscallMmap(addr, length, prot, flags, fd, offset uint64) int64 {
	// Ultra-early breadcrumb (works before console is initialized)
	console.Breadcrumb('M')

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

	// Log userspace mmap calls
	if isUserspace {
		console.KWriteString("[MMAP] US addr=")
		console.KPrintHex64(addr)
		console.KWriteString(" len=")
		console.KPrintHex64(alignedLength)
		console.KWriteString(" flags=")
		console.KPrintHex64(flags)
	}

	// Handle MAP_FIXED: must return the exact address or fail
	// MAP_FIXED can overwrite existing mappings (dangerous but that's the semantics)
	if isMapFixed && addr != 0 {
		if isUserspace && addr >= userMmapStart && addr+alignedLength <= userMmapEnd {
			// Remove any existing spans that overlap, then add new span
			removeSpan(addr, alignedLength)
			if !addSpan(addr, alignedLength) {
				console.KWriteString(" -> ENOMEM (no span slots)\r\n")
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
				if isUserspace {
					console.KWriteString(" -> ENOMEM (out of range)\r\n")
				}
				return -12 // ENOMEM - can't honor MAP_FIXED at this address
			}
		} else {
			if isUserspace {
				console.KWriteString(" -> ENOMEM (invalid addr)\r\n")
			}
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

	// Log result
	if isUserspace {
		console.KWriteString(" -> ")
		console.KPrintHex64(result)
		console.KWriteString("\r\n")
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

		// Check if this allocation would overlap any existing span
		// If so, return 0 (ENOMEM) - the low memory region is large enough
		// that this shouldn't happen in practice
		if findSpanOverlapEnd(currentPtr, aligned) != 0 {
			console.KWriteString("[BUMP] FAIL: overlaps span\r\n")
			return 0 // ENOMEM - allocation conflicts with reserved span
		}

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
