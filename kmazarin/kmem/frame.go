// Package kmem provides memory management for the kmazarin kernel.
// This includes physical frame allocation for heap pages and page table management.
package kmem

import (
	"unsafe"
)

// Page size (4KB)
const PageSize = 0x1000

// KmazarinTotalLimit is the 64MB limit for kmazarin kernel memory.
// This includes: static regions (code/data/bss) + TTBR1 region + PT pool + heap frames.
const KmazarinTotalLimit = 64 * 1024 * 1024 // 64 MB

// Frame pool boundaries are retrieved from runtime configuration (auxv).
// NO hardcoded addresses - everything comes from Cardinal at runtime.
//
// NOTE: The frame allocator now uses the unified page pool (unified_pool.go).
// The old separate kernel/user pools are deprecated.

// getRuntimeConfig is provided by main package via go:linkname.
func getRuntimeConfig() any

// Auxv-backed config values, set by archauxv() during runtime init.
// These replace the old getStartupConfigValue() which read from StartupParams.

//go:linkname kmazarinTTBR1L0Phys runtime.kmazarinTTBR1L0Phys
var kmazarinTTBR1L0Phys uintptr

//go:linkname kmazarinTTBR0L0Phys runtime.kmazarinTTBR0L0Phys
var kmazarinTTBR0L0Phys uintptr

//go:linkname kmazarinFramePoolStart runtime.kmazarinFramePoolStart
var kmazarinFramePoolStart uintptr

//go:linkname kmazarinFramePoolEnd runtime.kmazarinFramePoolEnd
var kmazarinFramePoolEnd uintptr

//go:linkname kmazarinUnifiedPoolStart runtime.kmazarinUnifiedPoolStart
var kmazarinUnifiedPoolStart uintptr

//go:linkname kmazarinUnifiedPoolEnd runtime.kmazarinUnifiedPoolEnd
var kmazarinUnifiedPoolEnd uintptr

//go:linkname kmazarinKmazarinSize runtime.kmazarinKmazarinSize
var kmazarinKmazarinSize uintptr

// InitFrameAllocator initializes the unified page pool.
// This should be called once during kmazarin startup.
// Frame pool boundaries come from Cardinal via RuntimeConfig.
//
//go:nosplit
func InitFrameAllocator() {
	// Initialize the unified pool which replaces the old separate pools
	InitUnifiedPool()
}

// AllocKernelFrame allocates a single physical frame from the unified pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should use Bzero4K after mapping if needed.
// Thread-safe via the unified pool's spinlock.
//
// The unified pool enforces a soft limit on kernel memory (default 64MB).
// Exceeding the soft limit generates a warning but does not fail allocation.
//
// For userspace allocations, use AllocUserFrame() instead.
//
//go:nosplit
func AllocKernelFrame() uintptr {
	return AllocPage(PageKernelHeap, 0)
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
// Returns kernel heap pages as "allocated" for backwards compatibility.
//
//go:nosplit
func GetFrameStats() (allocated, remaining uint64) {
	stats := GetPoolStats()
	allocated = stats.KernelHeapPages
	remaining = stats.RemainingPages
	return
}

// =============================================================================
// Userspace Frame Allocator
// =============================================================================
//
// Userspace pages are now allocated from the unified pool (unified_pool.go).
// The accounting tracks user vs kernel allocations separately.

// InitUserFrameAllocator initializes the userspace frame allocator.
// This is a no-op now since the unified pool handles all allocations.
// Kept for backwards compatibility with existing initialization code.
//
//go:nosplit
func InitUserFrameAllocator() {
	// Unified pool is initialized by InitFrameAllocator or lazily
	InitUnifiedPool()
}

// AllocUserFrame allocates a single physical frame from the unified pool.
// Returns the physical address of the frame, or 0 if the pool is exhausted.
// The frame is NOT zeroed - caller should zero it if needed.
// Thread-safe via the unified pool's spinlock.
//
//go:nosplit
func AllocUserFrame() uintptr {
	return AllocPage(PageUserHeap, pfContextShepherdID)
}

// GetUserFrameStats returns the current userspace frame allocator statistics.
//
//go:nosplit
func GetUserFrameStats() (allocated, remaining uint64) {
	stats := GetPoolStats()
	allocated = stats.UserPages
	remaining = stats.RemainingPages
	return
}

// runtimeConfigStruct is a local copy of RuntimeConfig to avoid circular imports.
// Must match the layout in shared/constants/runtime_config.go exactly.
type runtimeConfigStruct struct {
	Magic                  uint32
	Version                uint32
	DtbPhysAddr            uint64
	DtbSize                uint64
	DtbVirtAddr            uint64 // High-memory virtual address of DTB
	KmazarinPhysAddr       uint64
	KmazarinSize           uint64
	FramePoolStart         uint64
	FramePoolEnd           uint64
	KernelUartBase         uint64
	KernelGicBase          uint64
	TTBR1L0Phys            uint64
	TTBR0L0Phys            uint64
	StartupParamsAddr      uint64
	KernelVAOffset         uint64
	KernelPTPoolStart      uint64
	KernelPTPoolEnd        uint64
	KernelHeapStart        uint64
	KernelHeapEnd          uint64
	PageSize               uint64
	HWCap                  uint64
	G0StackBottom          uint64
	G0StackTop             uint64
	G0StackSize            uint64 // Size of g0 stack
	ExceptionStackBottom   uint64 // Bottom of exception stack
	ExceptionStackTop      uint64
	ExceptionStackSize     uint64
	G0StructAddr           uint64
	_reservedAsyncPreempt1 uint64 // Padding (was AsyncPreemptAddr — now unused)
	_reservedAsyncPreempt2 uint64 // Padding (was ReadyForAsyncPreempt — now unused)
	FramebufferPhysAddr    uint64
	FramebufferSize        uint64
	BootImagePhysAddr      uint64
	BootImageSize          uint64
	// New userspace memory region fields
	TotalRAMSize            uint64
	RAMBaseAddr             uint64
	UserspaceFramePoolStart uint64
	UserspaceFramePoolEnd   uint64
	UserspacePTPoolStart    uint64
	UserspacePTPoolEnd      uint64
	// Unified page pool (replaces separate kernel/user pools)
	UnifiedPoolStart uint64
	UnifiedPoolEnd   uint64
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
