
package main

import (
	"mazzy/kmazarin/dtb"
	"mazzy/shared/constants"
	"unsafe"
)

// RuntimeConfig holds the minimal configuration from Cardinal.
// Only 2 fields: DtbPhysAddr and KmazarinSize.
// All other values are derived from constants, CPU registers, or DTB.
//
// CRITICAL: Layout MUST match shared/constants.RuntimeConfig exactly.
type RuntimeConfig struct {
	DtbPhysAddr    uint64
	KmazarinSize   uint64
	FramePoolStart uint64
	FramePoolEnd   uint64
}

// runtimeConfig is the global configuration, lazily initialized on first access.
var runtimeConfig RuntimeConfig
var runtimeConfigInitialized bool

// Derived values cached after first computation
var (
	derivedInitialized bool

	// From DTB
	derivedRAMBase             uint64
	derivedRAMSize             uint64
	derivedUserspaceFramePoolStart uint64
	derivedUserspaceFramePoolEnd   uint64
	derivedUserspacePTPoolStart    uint64
	derivedUserspacePTPoolEnd      uint64

	// Computed from KmazarinSize
	derivedPTPoolStart uint64
	derivedPTPoolEnd   uint64
)

// getRuntimeConfigFromStartupParams reads the RuntimeConfig from StartupParams.
//
//go:nosplit
func getRuntimeConfigFromStartupParams() RuntimeConfig {
	configPtr := (*RuntimeConfig)(unsafe.Pointer(&StartupParams[RuntimeConfigOffset]))
	return *configPtr
}

// GetRuntimeConfig returns the global runtime configuration.
// Lazy-initializes on first call.
//
//go:nosplit
func GetRuntimeConfig() *RuntimeConfig {
	if !runtimeConfigInitialized {
		runtimeConfig = getRuntimeConfigFromStartupParams()
		runtimeConfigInitialized = true
	}
	return &runtimeConfig
}

// initDerivedValues computes all derived memory layout values from the
// minimal RuntimeConfig + constants + DTB. Called lazily on first access.
//
//go:nosplit
func initDerivedValues() {
	if derivedInitialized {
		return
	}

	cfg := GetRuntimeConfig()

	// Compute PT pool from KmazarinSize + KernelTextBase
	kmazarinEndVA := uint64(constants.KernelTextBase) + cfg.KmazarinSize
	// Page-align up
	alignedEnd := (kmazarinEndVA + 0xFFF) &^ 0xFFF
	// TTBR1 region (64KB) then PT pool (512KB)
	ttbr1RegionStart := alignedEnd
	_ = ttbr1RegionStart // TTBR1 region mapped by cardinal
	derivedPTPoolStart = alignedEnd + uint64(constants.KernelTTBR1RegionSize)
	derivedPTPoolEnd = derivedPTPoolStart + uint64(constants.KernelPTPoolSize)

	// DTB-derived values (RAM base/size, userspace pools)
	// DTB is at physical address, which is mapped via TTBR1 identity mapping
	dtbVirtAddr := cfg.DtbPhysAddr + uint64(constants.KernelMMIOOffset)
	ramBase, ramSize, ok := dtb.GetMemoryInfo(uintptr(dtbVirtAddr))
	if ok {
		derivedRAMBase = uint64(ramBase)
		derivedRAMSize = ramSize

		// Compute userspace memory regions (same formula as cardinal)
		userspaceTotal := ramSize - uint64(constants.KernelMemoryBudget)
		ptPoolSize := userspaceTotal / 513
		ptPoolSize = (ptPoolSize + 0xFFF) &^ 0xFFF

		kernelEnd := uint64(ramBase) + uint64(constants.KernelMemoryBudget)
		derivedUserspacePTPoolStart = kernelEnd
		derivedUserspacePTPoolEnd = kernelEnd + ptPoolSize
		derivedUserspaceFramePoolStart = derivedUserspacePTPoolEnd
		derivedUserspaceFramePoolEnd = uint64(ramBase) + ramSize
	}

	derivedInitialized = true
}

// GetUartBase returns the UART base address (constant).
//
//go:nosplit
func GetUartBase() uintptr {
	return uintptr(constants.KernelUartBase)
}

// GetAsyncPreemptAddr returns the address of the asyncPreemptWrapper.
// This is kmazarin's own assembly function, accessed via getAsyncPreemptWrapperAddr().
//
//go:nosplit
func GetAsyncPreemptAddr() uintptr {
	return getAsyncPreemptWrapperAddr()
}

// GetReadyForAsyncPreemptAddr returns the address of the readyForAsyncPreempt flag.
// This is a kmazarin global, so we return its address directly.
//
//go:nosplit
func GetReadyForAsyncPreemptAddr() uintptr {
	return uintptr(unsafe.Pointer(&readyForAsyncPreempt))
}

// GetDtbPhysAddr returns the DTB physical address from RuntimeConfig.
//
//go:nosplit
func GetDtbPhysAddr() uint64 {
	return GetRuntimeConfig().DtbPhysAddr
}

// GetKmazarinSize returns the kmazarin binary size from RuntimeConfig.
//
//go:nosplit
func GetKmazarinSize() uint64 {
	return GetRuntimeConfig().KmazarinSize
}

// GetPTPoolStart returns the computed PT pool start VA.
//
//go:nosplit
func GetPTPoolStart() uint64 {
	initDerivedValues()
	return derivedPTPoolStart
}

// GetPTPoolEnd returns the computed PT pool end VA.
//
//go:nosplit
func GetPTPoolEnd() uint64 {
	initDerivedValues()
	return derivedPTPoolEnd
}

// GetFramePoolStart returns the kernel frame pool start PA from RuntimeConfig.
//
//go:nosplit
func GetFramePoolStart() uint64 {
	return GetRuntimeConfig().FramePoolStart
}

// GetFramePoolEnd returns the kernel frame pool end PA from RuntimeConfig.
//
//go:nosplit
func GetFramePoolEnd() uint64 {
	return GetRuntimeConfig().FramePoolEnd
}

// GetTotalRAMSize returns the total RAM size from DTB.
//
//go:nosplit
func GetTotalRAMSize() uint64 {
	initDerivedValues()
	return derivedRAMSize
}

// GetRAMBaseAddr returns the RAM base physical address from DTB.
//
//go:nosplit
func GetRAMBaseAddr() uint64 {
	initDerivedValues()
	return derivedRAMBase
}

// GetUserspaceFramePoolStart returns the userspace frame pool start PA.
//
//go:nosplit
func GetUserspaceFramePoolStart() uint64 {
	initDerivedValues()
	return derivedUserspaceFramePoolStart
}

// GetUserspaceFramePoolEnd returns the userspace frame pool end PA.
//
//go:nosplit
func GetUserspaceFramePoolEnd() uint64 {
	initDerivedValues()
	return derivedUserspaceFramePoolEnd
}

// GetUserspacePTPoolStart returns the userspace PT pool start PA.
//
//go:nosplit
func GetUserspacePTPoolStart() uint64 {
	initDerivedValues()
	return derivedUserspacePTPoolStart
}

// GetUserspacePTPoolEnd returns the userspace PT pool end PA.
//
//go:nosplit
func GetUserspacePTPoolEnd() uint64 {
	initDerivedValues()
	return derivedUserspacePTPoolEnd
}

// fullConfig is the expanded view of all memory layout values needed by
// kmem and ksyscall packages. This is returned via the getRuntimeConfig()
// linkname interface to maintain compatibility with those packages' local
// runtimeConfigStruct types.
type fullConfig struct {
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

var cachedFullConfig fullConfig
var fullConfigInitialized bool

// getFullConfig returns a pointer to the fully-populated config struct
// for use by kmem and ksyscall packages.
//
//go:nosplit
func getFullConfig() *fullConfig {
	if fullConfigInitialized {
		return &cachedFullConfig
	}
	populateFullConfig()
	return &cachedFullConfig
}

// populateFullConfig fills the global cachedFullConfig struct.
// Separated from getFullConfig to minimize stack frame in the nosplit chain.
//
//go:nosplit
func populateFullConfig() {
	initDerivedValues()

	cfg := GetRuntimeConfig()

	cachedFullConfig.KernelVAOffset = uint64(constants.KernelMMIOOffset)
	cachedFullConfig.KmazarinSize = cfg.KmazarinSize
	cachedFullConfig.KmazarinPhysAddr = uint64(constants.KmazarinLoadAddr)
	cachedFullConfig.FramePoolStart = cfg.FramePoolStart
	cachedFullConfig.FramePoolEnd = cfg.FramePoolEnd
	cachedFullConfig.KernelPTPoolStart = derivedPTPoolStart
	cachedFullConfig.KernelPTPoolEnd = derivedPTPoolEnd
	cachedFullConfig.KernelHeapStart = uint64(constants.KernelHeapStart)
	cachedFullConfig.KernelHeapEnd = uint64(constants.KernelHeapEnd)
	cachedFullConfig.G0StackBottom = uint64(constants.KernelG0StackBottom)
	cachedFullConfig.G0StackTop = uint64(constants.KernelG0StackTop)
	cachedFullConfig.G0StackSize = uint64(constants.KernelG0StackSize)
	cachedFullConfig.ExceptionStackBottom = uint64(constants.KernelExcStackBottom)
	cachedFullConfig.ExceptionStackTop = uint64(constants.KernelExcStackTop)
	cachedFullConfig.ExceptionStackSize = uint64(constants.KernelExcStackSize)
	cachedFullConfig.FramebufferPhysAddr = uint64(constants.FramebufferPhysAddr)
	cachedFullConfig.FramebufferSize = uint64(constants.FramebufferSize)
	cachedFullConfig.TotalRAMSize = derivedRAMSize
	cachedFullConfig.RAMBaseAddr = derivedRAMBase
	cachedFullConfig.UserspaceFramePoolStart = derivedUserspaceFramePoolStart
	cachedFullConfig.UserspaceFramePoolEnd = derivedUserspaceFramePoolEnd
	cachedFullConfig.UserspacePTPoolStart = derivedUserspacePTPoolStart
	cachedFullConfig.UserspacePTPoolEnd = derivedUserspacePTPoolEnd
	cachedFullConfig.DtbPhysAddr = cfg.DtbPhysAddr

	fullConfigInitialized = true
}
