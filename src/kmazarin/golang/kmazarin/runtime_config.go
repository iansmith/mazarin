//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// RuntimeConfig holds configuration values pre-populated by Cardinal.
// Cardinal writes this structure to StartupParams before jumping to kmazarin,
// so all values are immediately available without parsing or computation.
//
// CRITICAL: This structure layout MUST match shared/constants.RuntimeConfig exactly.
type RuntimeConfig struct {
	// Magic marker to detect valid initialization (0x4B4D5A52 = "KMZR")
	Magic   uint32
	Version uint32

	// Memory layout parameters from Cardinal
	DtbPhysAddr       uint64 // Physical address of DTB
	DtbSize           uint64 // Size of DTB
	KmazarinPhysAddr  uint64 // Physical base of kmazarin binary
	KmazarinSize      uint64 // Size of kmazarin binary
	FramePoolStart    uint64 // Physical frame pool start
	FramePoolEnd      uint64 // Physical frame pool end
	KernelUartBase    uint64 // High-memory UART VA (0xFFFFFFFF09000000)
	KernelGicBase     uint64 // High-memory GIC VA
	TTBR1L0Phys       uint64 // Physical address of TTBR1 L0
	StartupParamsAddr uint64 // Address of StartupParams buffer

	// Derived values (computed by Cardinal)
	KernelVAOffset    uint64 // Kernel VA offset (0xFFFFFFFF00000000)
	KernelPTPoolStart uint64 // PT pool start VA
	KernelPTPoolEnd   uint64 // PT pool end VA
	KernelHeapStart   uint64 // Heap start VA
	KernelHeapEnd     uint64 // Heap end VA

	// Standard values
	PageSize uint64 // Page size (4096)
	HWCap    uint64 // ARM64 hardware capabilities

	// Stack boundaries (Cardinal bootstrap stacks)
	G0StackBottom      uint64 // Bottom of g0 stack (SP_EL0)
	G0StackTop         uint64 // Top of g0 stack (SP_EL0)
	ExceptionStackTop  uint64 // Top of exception stack (SP_EL1)
	ExceptionStackSize uint64 // Exception stack size
}

// runtimeConfig is the global configuration, populated at package init time.
// CRITICAL: Must be initialized BEFORE Go runtime starts (which calls mmap syscalls).
var runtimeConfig RuntimeConfig
var runtimeConfigInitialized bool

// init reads the pre-populated RuntimeConfig from StartupParams.
// This runs at package initialization time, before the Go runtime starts.
func init() {
	// Read the RuntimeConfig that Cardinal wrote to StartupParams[0]
	// StartupParams is a BSS global, so its address is known at link time
	configPtr := (*RuntimeConfig)(unsafe.Pointer(&StartupParams[RuntimeConfigOffset]))

	// Verify magic number
	const expectedMagic = uint32(0x4B4D5A52) // "KMZR"
	if configPtr.Magic != expectedMagic {
		// Fatal error - config not initialized by Cardinal
		// We can't do much here since UART might not be set up yet
		// Just halt
		for {
		}
	}

	// Copy to our global (avoids needing to cast every time)
	runtimeConfig = *configPtr
	runtimeConfigInitialized = true
}

// GetRuntimeConfig returns the global runtime configuration.
// MUST be called after initRuntimeConfigFromAuxv() has been called (in init()).
// NO lazy initialization - this is required for nosplit stack limits.
//
//go:nosplit
//go:linkname GetRuntimeConfig
func GetRuntimeConfig() *RuntimeConfig {
	return &runtimeConfig
}

// GetUartBase returns the UART base address from runtime config.
// This is provided as a helper for other packages via go:linkname.
//
//go:nosplit
//go:linkname GetUartBase
func GetUartBase() uintptr {
	return uintptr(GetRuntimeConfig().KernelUartBase)
}
