
package main

import (
	"mazzy/kmazarin/dtb"
	"mazzy/shared/constants"
	_ "unsafe" // for go:linkname
)

// Auxv-backed config values, set by archauxv() during runtime init.
// These replace the old getStartupConfigValue() which read from StartupParams.

//go:linkname kmazarinDtbPhysAddr runtime.kmazarinDtbPhysAddr
var kmazarinDtbPhysAddr uintptr

//go:linkname kmazarinKmazarinSize runtime.kmazarinKmazarinSize
var kmazarinKmazarinSize uintptr

//go:linkname kmazarinFramePoolStart runtime.kmazarinFramePoolStart
var kmazarinFramePoolStart uintptr

//go:linkname kmazarinFramePoolEnd runtime.kmazarinFramePoolEnd
var kmazarinFramePoolEnd uintptr

//go:linkname kmazarinCPUCount runtime.kmazarinCPUCount
var kmazarinCPUCount uintptr

//go:linkname kmazarinRAMBase runtime.kmazarinRAMBase
var kmazarinRAMBase uintptr

//go:linkname kmazarinRAMSize runtime.kmazarinRAMSize
var kmazarinRAMSize uintptr

//go:linkname kmazarinKernelBudgetMB runtime.kmazarinKernelBudgetMB
var kmazarinKernelBudgetMB uintptr

//go:linkname kmazarinUnifiedPoolStart runtime.kmazarinUnifiedPoolStart
var kmazarinUnifiedPoolStart uintptr

//go:linkname kmazarinUnifiedPoolEnd runtime.kmazarinUnifiedPoolEnd
var kmazarinUnifiedPoolEnd uintptr

//go:linkname kmazarinSyscallReady runtime.kmazarinSyscallReady
var kmazarinSyscallReady uint64

// nsPerTickX256 is the fixed-point (×256) nanoseconds-per-tick conversion
// factor used by runtime·nanotime1 (assembly). Defined in each arch's
// sys_linux_<arch>.s with a default for the expected QEMU timer frequency.
// Updated by initTimerFrequency() when the real frequency is discovered.
//
//go:linkname nsPerTickX256 runtime.nsPerTickX256
var nsPerTickX256 uint64

// SetSyscallReady marks the overlay syscall handlers as operational.
// Called after SetVBAR installs kmazarin's exception vectors.
// This enables the usleep/futex overlays to issue real SVCs instead of
// spin+yield, providing proper blocking sleep and futex behavior.
//
//go:nosplit
func SetSyscallReady() {
	kmazarinSyscallReady = 1
}

// kmazarinGCWaiting returns true when the GC is waiting to stop the world.
// Called from ProcessDeadlinesTopHalf (250Hz tick) to wake sleeping kernel
// threads so sysmon can participate in STW coordination promptly.
//
//go:linkname kmazarinGCWaiting runtime.kmazarinGCWaiting
func kmazarinGCWaiting() bool

// RuntimeConfig holds the minimal configuration from the bootloader.
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

	// From DTB or auxv
	derivedRAMBase                 uint64
	derivedRAMSize                 uint64
	derivedUserspaceFramePoolStart uint64
	derivedUserspaceFramePoolEnd   uint64
	derivedUserspacePTPoolStart    uint64
	derivedUserspacePTPoolEnd      uint64

	// Computed from KmazarinSize
	derivedPTPoolStart uint64
	derivedPTPoolEnd   uint64
)

// GetRuntimeConfig returns the global runtime configuration.
// Lazy-initializes from auxv-backed vars on first call.
//
//go:nosplit
func GetRuntimeConfig() *RuntimeConfig {
	if !runtimeConfigInitialized {
		runtimeConfig.DtbPhysAddr = uint64(kmazarinDtbPhysAddr)
		runtimeConfig.KmazarinSize = uint64(kmazarinKmazarinSize)
		runtimeConfig.FramePoolStart = uint64(kmazarinFramePoolStart)
		runtimeConfig.FramePoolEnd = uint64(kmazarinFramePoolEnd)
		runtimeConfigInitialized = true
	}
	return &runtimeConfig
}

// initDerivedValues computes all derived memory layout values from the
// auxv-backed config + constants + DTB. Called lazily on first access.
//
//go:nosplit
func initDerivedValues() {
	if derivedInitialized {
		return
	}

	kmazarinSize := uint64(kmazarinKmazarinSize)
	dtbPhysAddr := uint64(kmazarinDtbPhysAddr)

	// Compute PT pool from KmazarinSize + KernelTextBase
	kmazarinEndVA := uint64(constants.KernelTextBase) + kmazarinSize
	// Page-align up
	alignedEnd := (kmazarinEndVA + 0xFFF) &^ 0xFFF
	// TTBR1 region (64KB) then PT pool (512KB)
	derivedPTPoolStart = alignedEnd + uint64(constants.KernelTTBR1RegionSize)
	derivedPTPoolEnd = derivedPTPoolStart + uint64(constants.KernelPTPoolSize)

	// RAM base/size: prefer auxv values, fall back to DTB parsing
	if kmazarinRAMBase != 0 && kmazarinRAMSize != 0 {
		derivedRAMBase = uint64(kmazarinRAMBase)
		derivedRAMSize = uint64(kmazarinRAMSize)
	} else if dtbPhysAddr != 0 {
		// Fallback: parse DTB for RAM info
		dtbVirtAddr := dtbPhysAddr + uint64(constants.KernelVAOffset)
		ramBase, ramSize, ok := dtb.GetMemoryInfo(uintptr(dtbVirtAddr))
		if ok {
			derivedRAMBase = uint64(ramBase)
			derivedRAMSize = ramSize
		}
	}

	if derivedRAMBase != 0 && derivedRAMSize != 0 {
		// Compute userspace memory regions (same formula as cardinal)
		userspaceTotal := derivedRAMSize - uint64(constants.KernelMemoryBudget)
		ptPoolSize := userspaceTotal / 513
		ptPoolSize = (ptPoolSize + 0xFFF) &^ 0xFFF

		kernelEnd := derivedRAMBase + uint64(constants.KernelMemoryBudget)
		derivedUserspacePTPoolStart = kernelEnd
		derivedUserspacePTPoolEnd = kernelEnd + ptPoolSize
		derivedUserspaceFramePoolStart = derivedUserspacePTPoolEnd
		derivedUserspaceFramePoolEnd = derivedRAMBase + derivedRAMSize
	}

	derivedInitialized = true
}

// GetUartBase returns the UART base address (constant).
//
//go:nosplit
func GetUartBase() uintptr {
	return uintptr(constants.KernelUartBase)
}

// GetDtbPhysAddr returns the DTB physical address from auxv.
//
//go:nosplit
func GetDtbPhysAddr() uint64 {
	return uint64(kmazarinDtbPhysAddr)
}

// GetKmazarinSize returns the kmazarin binary size from auxv.
//
//go:nosplit
func GetKmazarinSize() uint64 {
	return uint64(kmazarinKmazarinSize)
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

// GetFramePoolStart returns the kernel frame pool start PA from auxv.
//
//go:nosplit
func GetFramePoolStart() uint64 {
	return uint64(kmazarinFramePoolStart)
}

// GetFramePoolEnd returns the kernel frame pool end PA from auxv.
//
//go:nosplit
func GetFramePoolEnd() uint64 {
	return uint64(kmazarinFramePoolEnd)
}

// GetTotalRAMSize returns the total RAM size.
//
//go:nosplit
func GetTotalRAMSize() uint64 {
	initDerivedValues()
	return derivedRAMSize
}

// GetRAMBaseAddr returns the RAM base physical address.
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

// GetCPUCount returns the number of CPUs from auxv.
// Returns 1 if the value is not set (for backward compatibility).
//
//go:nosplit
func GetCPUCount() uint64 {
	count := uint64(kmazarinCPUCount)
	if count == 0 {
		return 1 // Default to 1 CPU if not set
	}
	return count
}

// GetKernelBudgetMB returns the kernel memory budget warning threshold in MB from auxv.
// Returns 0 if not set (buddy.go will use its compiled-in default of 128MB).
//
//go:nosplit
func GetKernelBudgetMB() uint64 {
	return uint64(kmazarinKernelBudgetMB)
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

	cachedFullConfig.KernelVAOffset = uint64(constants.KernelVAOffset)
	cachedFullConfig.KmazarinSize = uint64(kmazarinKmazarinSize)
	cachedFullConfig.KmazarinPhysAddr = uint64(constants.KmazarinLoadAddr)
	cachedFullConfig.FramePoolStart = uint64(kmazarinFramePoolStart)
	cachedFullConfig.FramePoolEnd = uint64(kmazarinFramePoolEnd)
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
	cachedFullConfig.DtbPhysAddr = uint64(kmazarinDtbPhysAddr)

	fullConfigInitialized = true
}
