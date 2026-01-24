
// Package kmem provides memory management for the kmazarin kernel.
// This includes physical frame allocation for heap pages and page table management.
package kmem

import (
	"mazzy/kmazarin/console"
	"sync/atomic"
	"unsafe"
)

// Page size (4KB)
const PageSize = 0x1000

// KmazarinTotalLimit is the 64MB limit for kmazarin kernel memory.
// This includes: static regions (code/data/bss) + TTBR1 region + PT pool + heap frames.
const KmazarinTotalLimit = 64 * 1024 * 1024 // 64 MB

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

// AllocKernelFrame allocates a single physical frame from the kernel frame pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should use Bzero4K after mapping if needed.
// Thread-safe: uses atomic operations for concurrent allocation.
//
// CRITICAL: This function enforces the 64MB total kernel memory limit.
// If allocation would exceed the limit, it panics with "kernel memory exhausted".
//
// For userspace allocations, use AllocUserFrame() instead.
//
//go:nosplit
func AllocKernelFrame() uintptr {
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

	// Check 64MB limit BEFORE allocating
	// Total kernel memory = KmazarinSize (static) + overhead + (allocated+1)*PageSize
	// Overhead = TTBR1 region (64KB) + PT pool (512KB) = 576KB = 0x90000
	cfg := getRuntimeConfigTyped()
	const overheadBytes = 0x90000 // TTBR1 region + PT pool
	currentAllocated := atomic.LoadUint64(&frameAllocator.allocated)
	totalAfterAlloc := cfg.KmazarinSize + overheadBytes + (currentAllocated+1)*PageSize

	if totalAfterAlloc > KmazarinTotalLimit {
		uartPuts("\r\n*** KERNEL PANIC: kernel memory exhausted (64MB limit) ***\r\n")
		uartPuts("  KmazarinSize: ")
		uartPutHex64(cfg.KmazarinSize)
		uartPuts("\r\n  Overhead: ")
		uartPutHex64(overheadBytes)
		uartPuts("\r\n  Heap frames: ")
		uartPutHex64(currentAllocated)
		uartPuts("\r\n  Total: ")
		uartPutHex64(totalAfterAlloc)
		uartPuts(" > 64MB (")
		uartPutHex64(KmazarinTotalLimit)
		uartPuts(")\r\n")
		// Infinite loop to halt
		for {
		}
	}

	// Atomically allocate a frame
	for {
		currentNext := atomic.LoadUintptr(&frameAllocator.nextFrame)
		endFrame := atomic.LoadUintptr(&frameAllocator.endFrame)

		if currentNext >= endFrame {
			uartPuts("\r\n*** KERNEL PANIC: frame pool exhausted ***\r\n")
			// Infinite loop to halt
			for {
			}
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

// =============================================================================
// Userspace Frame Allocator
// =============================================================================
//
// Separate frame allocator for userspace pages. This keeps userspace memory
// completely separate from kernel memory for security and resource management.

// userFrameAllocator tracks the userspace frame pool allocation state
var userFrameAllocator frameAllocatorState

// userInitialized tracks whether the userspace frame allocator has been initialized
var userInitialized uint32

// InitUserFrameAllocator initializes the userspace frame allocator.
// This should be called once during kmazarin startup after kernel frame allocator.
// Frame pool boundaries come from Cardinal via RuntimeConfig.
//
//go:nosplit
func InitUserFrameAllocator() {
	cfg := getRuntimeConfigTyped()

	// Check if userspace frame pool is configured
	if cfg.UserspaceFramePoolStart == 0 || cfg.UserspaceFramePoolEnd == 0 {
		uartPuts("[kmem] WARNING: Userspace frame pool not configured\r\n")
		return
	}

	userFrameAllocator.nextFrame = uintptr(cfg.UserspaceFramePoolStart)
	userFrameAllocator.endFrame = uintptr(cfg.UserspaceFramePoolEnd)
	userFrameAllocator.allocated = 0
	atomic.StoreUint32(&userInitialized, 1)

	poolSize := cfg.UserspaceFramePoolEnd - cfg.UserspaceFramePoolStart
	uartPuts("[kmem] Userspace frame pool: ")
	uartPutHex64(cfg.UserspaceFramePoolStart)
	uartPuts(" - ")
	uartPutHex64(cfg.UserspaceFramePoolEnd)
	uartPuts(" (")
	uartPutHex64(poolSize / (1024 * 1024))
	uartPuts(" MB)\r\n")
}

// AllocUserFrame allocates a single physical frame from the userspace frame pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should zero it if needed.
// Thread-safe: uses atomic operations for concurrent allocation.
//
//go:nosplit
func AllocUserFrame() uintptr {
	// Lazy initialization from runtime config (atomic check)
	if atomic.LoadUint32(&userInitialized) == 0 {
		// Use compare-and-swap to ensure only one thread initializes
		if atomic.CompareAndSwapUint32(&userInitialized, 0, 1) {
			cfg := getRuntimeConfigTyped()
			if cfg.UserspaceFramePoolStart == 0 || cfg.UserspaceFramePoolEnd == 0 {
				// Not configured - fall back to kernel pool (temporary)
				uartPuts("[kmem] WARN: Using kernel pool for userspace (legacy mode)\r\n")
				atomic.StoreUint32(&userInitialized, 2) // Mark as not available
				return AllocKernelFrame() // Fall back to kernel allocator
			}
			atomic.StoreUintptr(&userFrameAllocator.nextFrame, uintptr(cfg.UserspaceFramePoolStart))
			atomic.StoreUintptr(&userFrameAllocator.endFrame, uintptr(cfg.UserspaceFramePoolEnd))
			atomic.StoreUint64(&userFrameAllocator.allocated, 0)
		} else {
			// Another thread won the race, wait for it to complete initialization
			for atomic.LoadUint32(&userInitialized) == 0 {
				// Spin wait
			}
		}
	}

	// Check if userspace pool is not available (flag=2 means not configured)
	if atomic.LoadUint32(&userInitialized) == 2 {
		return AllocKernelFrame() // Fall back to kernel allocator
	}

	// Atomically allocate a frame from userspace pool
	for {
		currentNext := atomic.LoadUintptr(&userFrameAllocator.nextFrame)
		endFrame := atomic.LoadUintptr(&userFrameAllocator.endFrame)

		if currentNext >= endFrame {
			uartPuts("[kmem] Userspace OOM!\r\n")
			return 0
		}

		newNext := currentNext + PageSize
		if atomic.CompareAndSwapUintptr(&userFrameAllocator.nextFrame, currentNext, newNext) {
			// Successfully allocated frame
			atomic.AddUint64(&userFrameAllocator.allocated, 1)
			return currentNext
		}
		// CAS failed, another thread allocated, retry
	}
}

// GetUserFrameStats returns the current userspace frame allocator statistics.
//
//go:nosplit
func GetUserFrameStats() (allocated, remaining uint64) {
	cfg := getRuntimeConfigTyped()
	poolSize := cfg.UserspaceFramePoolEnd - cfg.UserspaceFramePoolStart
	allocated = userFrameAllocator.allocated
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
	G0StructAddr         uint64
	AsyncPreemptAddr     uint64
	ReadyForAsyncPreempt uint64
	FramebufferPhysAddr  uint64
	FramebufferSize      uint64
	BootImagePhysAddr    uint64
	BootImageSize        uint64
	// New userspace memory region fields
	TotalRAMSize            uint64
	RAMBaseAddr             uint64
	UserspaceFramePoolStart uint64
	UserspaceFramePoolEnd   uint64
	UserspacePTPoolStart    uint64
	UserspacePTPoolEnd      uint64
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

// RuntimeConfig is an exported wrapper for accessing runtime configuration.
// Used by other packages (like idalloc) that need kernel VA offset etc.
type RuntimeConfig struct {
	KernelVAOffset uint64
}

// GetRuntimeConfig returns an exported view of the runtime configuration.
// Returns nil if config is not yet available.
//
//go:nosplit
func GetRuntimeConfig() *RuntimeConfig {
	cfg := getRuntimeConfigTyped()
	if cfg == nil {
		return nil
	}
	return &RuntimeConfig{
		KernelVAOffset: cfg.KernelVAOffset,
	}
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
