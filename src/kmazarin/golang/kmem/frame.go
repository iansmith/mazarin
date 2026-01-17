//go:build qemuvirt && aarch64

// Package kmem provides memory management for the kmazarin kernel.
// This includes physical frame allocation for heap pages and page table management.
package kmem

import (
	"kmazarin/console"
	"sync/atomic"
	"unsafe"
)

// Page size (4KB)
const PageSize = 0x1000

// Frame pool boundaries are retrieved from runtime configuration (auxv).
// NO hardcoded addresses - everything comes from Cardinal at runtime.

// frameAllocatorState tracks the kernel frame pool allocation state
type frameAllocatorState struct {
	nextFrame uintptr // Next physical frame to allocate
	endFrame  uintptr // End of frame pool (exclusive)
	allocated uint64  // Number of frames allocated
}

// frameAllocator is the global kernel frame allocator
// NOTE: nextFrame/endFrame are lazy-initialized from runtime config (auxv).
var frameAllocator frameAllocatorState

// initialized tracks whether the frame allocator has been initialized
// Uses atomic operations to prevent races during lazy initialization
var initialized uint32

// getRuntimeConfig is provided by main package via go:linkname.
func getRuntimeConfig() interface{}

// InitFrameAllocator initializes the kernel frame allocator.
// This should be called once during kmazarin startup.
// Frame pool boundaries come from Cardinal via RuntimeConfig.
//
//go:nosplit
func InitFrameAllocator() {
	// Get runtime config (pre-populated by Cardinal)
	cfg := getRuntimeConfigTyped()

	frameAllocator.nextFrame = uintptr(cfg.FramePoolStart)
	frameAllocator.endFrame = uintptr(cfg.FramePoolEnd)
	frameAllocator.allocated = 0
	atomic.StoreUint32(&initialized, 1)

	_ = cfg.FramePoolEnd - cfg.FramePoolStart // Pool size calculated but not printed
}

// AllocFrame allocates a single physical frame from the kernel frame pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should zero it if needed.
// Thread-safe: uses atomic operations for concurrent allocation.
//
//go:nosplit
func AllocFrame() uintptr {
	// Lazy initialization from runtime config (atomic check)
	if atomic.LoadUint32(&initialized) == 0 {
		// Use compare-and-swap to ensure only one thread initializes
		if atomic.CompareAndSwapUint32(&initialized, 0, 1) {
			cfg := getRuntimeConfigTyped()
			atomic.StoreUintptr(&frameAllocator.nextFrame, uintptr(cfg.FramePoolStart))
			atomic.StoreUintptr(&frameAllocator.endFrame, uintptr(cfg.FramePoolEnd))
			atomic.StoreUint64(&frameAllocator.allocated, 0)
		} else {
			// Another thread won the race, wait for it to complete initialization
			for atomic.LoadUint32(&initialized) == 0 {
				// Spin wait
			}
		}
	}

	// Atomically allocate a frame
	for {
		currentNext := atomic.LoadUintptr(&frameAllocator.nextFrame)
		endFrame := atomic.LoadUintptr(&frameAllocator.endFrame)

		if currentNext >= endFrame {
			uartPuts("[kmem] OOM!\r\n")
			return 0
		}

		newNext := currentNext + PageSize
		if atomic.CompareAndSwapUintptr(&frameAllocator.nextFrame, currentNext, newNext) {
			// Successfully allocated frame
			atomic.AddUint64(&frameAllocator.allocated, 1)
			return currentNext
		}
		// CAS failed, another thread allocated, retry
	}
}

// ZeroFrame zeros a physical frame at the given address.
// The address must be a valid physical address in kernel space.
//
//go:nosplit
func ZeroFrame(physAddr uintptr) {
	// We need to access the physical memory. Since we're in high memory,
	// we need to use the kernel VA offset to access physical memory.
	cfg := getRuntimeConfigTyped()
	va := physAddr + uintptr(cfg.KernelVAOffset)

	// Zero 4KB in 8-byte chunks (512 iterations)
	ptr := (*[512]uint64)(unsafe.Pointer(va))
	for i := 0; i < 512; i++ {
		ptr[i] = 0
	}
}

// GetFrameStats returns the current frame allocator statistics.
//
//go:nosplit
func GetFrameStats() (allocated, remaining uint64) {
	cfg := getRuntimeConfigTyped()
	poolSize := cfg.FramePoolEnd - cfg.FramePoolStart
	allocated = frameAllocator.allocated
	totalFrames := uint64(poolSize / PageSize)
	remaining = totalFrames - allocated
	return
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

// uartPuts writes a string to console
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func uartPuts(s string) {
	console.KWriteString(s)
}

// uartPutHex64 writes a 64-bit hex value to console
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func uartPutHex64(val uint64) {
	console.KPrintHex64(val)
}
