//go:build qemuvirt && aarch64

// Package kmem provides memory management for the kmazarin kernel.
// This includes physical frame allocation for heap pages and page table management.
package kmem

import (
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
var initialized bool

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
	initialized = true

	poolSize := cfg.FramePoolEnd - cfg.FramePoolStart
	uartPuts("[kmem] Frame allocator initialized\r\n")
	uartPuts("  Pool: 0x")
	uartPutHex64(cfg.FramePoolStart)
	uartPuts(" - 0x")
	uartPutHex64(cfg.FramePoolEnd)
	uartPuts("\r\n")
	uartPuts("  Size: ")
	uartPutHex64(poolSize / (1024 * 1024))
	uartPuts(" MB (")
	uartPutHex64(poolSize / PageSize)
	uartPuts(" frames)\r\n")
}

// AllocFrame allocates a single physical frame from the kernel frame pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should zero it if needed.
//
//go:nosplit
func AllocFrame() uintptr {
	// Lazy initialization from runtime config
	if !initialized {
		cfg := getRuntimeConfigTyped()
		frameAllocator.nextFrame = uintptr(cfg.FramePoolStart)
		frameAllocator.endFrame = uintptr(cfg.FramePoolEnd)
		frameAllocator.allocated = 0
		initialized = true
	}

	if frameAllocator.nextFrame >= frameAllocator.endFrame {
		uartPuts("[kmem] OOM!\r\n")
		return 0
	}

	frame := frameAllocator.nextFrame
	frameAllocator.nextFrame += PageSize
	frameAllocator.allocated++

	return frame
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
// Must match the layout in kmazarin/runtime_config.go exactly.
type runtimeConfigStruct struct {
	Magic             uint32
	Version           uint32
	DtbPhysAddr       uint64
	DtbSize           uint64
	KmazarinPhysAddr  uint64
	KmazarinSize      uint64
	FramePoolStart    uint64
	FramePoolEnd      uint64
	KernelUartBase    uint64
	KernelGicBase     uint64
	TTBR1L0Phys       uint64
	StartupParamsAddr uint64
	KernelVAOffset    uint64
	KernelPTPoolStart uint64
	KernelPTPoolEnd   uint64
	KernelHeapStart   uint64
	KernelHeapEnd     uint64
	PageSize          uint64
	HWCap             uint64
	G0StackBottom      uint64
	G0StackTop         uint64
	ExceptionStackTop  uint64
	ExceptionStackSize uint64
}

// getRuntimeConfigTyped returns the runtime config with proper type.
// This helper avoids the interface{} indirection in hot paths.
//
//go:nosplit
func getRuntimeConfigTyped() *runtimeConfigStruct {
	cfgInterface := getRuntimeConfig()
	return (*runtimeConfigStruct)(unsafe.Pointer(&cfgInterface))
}

// uartPuts writes a string directly to UART
//
//go:nosplit
func uartPuts(s string) {
	cfg := getRuntimeConfigTyped()
	uartBase := uintptr(cfg.KernelUartBase)
	for i := 0; i < len(s); i++ {
		*(*byte)(unsafe.Pointer(uartBase)) = s[i]
	}
}

// uartPutHex64 writes a 64-bit hex value to UART
//
//go:nosplit
func uartPutHex64(val uint64) {
	cfg := getRuntimeConfigTyped()
	uartBase := uintptr(cfg.KernelUartBase)
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[nibble]
	}
}
