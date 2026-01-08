//go:build qemuvirt && aarch64

package ksyscall

import "unsafe"

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
	// DEBUG: Log mmap request
	uartPutsDirect("mmap(len=0x")
	uartPutHex64Direct(length)
	uartPutsDirect(") ")

	// For now, ignore addr hint and just allocate from a bump pointer
	// This is very simplified - real mmap needs:
	// - Respect MAP_FIXED if addr != 0 and MAP_FIXED is set
	// - Track allocated regions (spans)
	// - Handle file-backed mappings (fd != -1)
	// - Handle different protections

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	// Get memory from bump allocator (returns high-memory VA)
	result := bumpAlloc(alignedLength)

	// DEBUG: Log result
	if result == 0 {
		uartPutsDirect("-> FAIL\r\n")
		return -12 // ENOMEM
	}

	uartPutsDirect("-> 0x")
	uartPutHex64Direct(result)
	uartPutsDirect("\r\n")

	return int64(result)
}

// Simple bump allocator for mmap
// Allocates from the kernel heap VA range (high memory, demand-paged)
var bumpPointer uint64 = 0
var bumpInitialized bool

//go:nosplit
func bumpAlloc(size uint64) uint64 {
	// Lazy initialization from runtime config
	if !bumpInitialized {
		cfg := getRuntimeConfigTyped()
		bumpPointer = uint64(cfg.KernelHeapStart)
		bumpInitialized = true
	}

	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Check if we have space in the heap range
	cfg := getRuntimeConfigTyped()
	if bumpPointer+aligned > uint64(cfg.KernelHeapEnd) {
		return 0 // Out of heap VA space
	}

	result := bumpPointer
	bumpPointer += aligned

	return result
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
