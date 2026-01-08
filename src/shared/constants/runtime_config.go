// runtime_config.go - Shared runtime configuration structure
//
// This structure is populated by Cardinal and consumed by Kmazarin.
// It contains all runtime-derived memory layout information needed by Kmazarin.

package constants

// RuntimeConfig holds configuration values derived from Cardinal's boot process.
// Cardinal populates this structure before jumping to Kmazarin, so all values
// are immediately available without needing to parse auxv or perform calculations.
//
// CRITICAL: This struct is shared between Cardinal and Kmazarin. Any changes
// must be coordinated between both codebases to maintain compatibility.
type RuntimeConfig struct {
	// Magic marker to detect valid initialization (0x4B4D5A52 = "KMZR")
	Magic uint32

	// Version for future compatibility (currently 1)
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
	StartupParamsAddr uint64 // Address of StartupParams buffer itself

	// Derived values (computed by Cardinal)
	KernelVAOffset    uint64 // Kernel VA offset (0xFFFFFFFF00000000)
	KernelPTPoolStart uint64 // PT pool start VA (0xFFFFFFFF419D0000)
	KernelPTPoolEnd   uint64 // PT pool end VA (0xFFFFFFFF41A00000)
	KernelHeapStart   uint64 // Heap start VA (0xFFFFFFFF41A00000)
	KernelHeapEnd     uint64 // Heap end VA (0xFFFFFFFF45800000)

	// Standard auxv values
	PageSize uint64 // AT_PAGESZ - typically 4096
	HWCap    uint64 // AT_HWCAP - ARM64 hardware capabilities

	// Stack boundaries (Cardinal bootstrap stacks)
	G0StackBottom      uint64 // Bottom of g0 stack (SP_EL0)
	G0StackTop         uint64 // Top of g0 stack (SP_EL0)
	ExceptionStackTop  uint64 // Top of exception stack (SP_EL1)
	ExceptionStackSize uint64 // Exception stack size
}

// RuntimeConfigMagic is the expected magic value for a valid RuntimeConfig
const RuntimeConfigMagic = 0x4B4D5A52 // "KMZR" in ASCII
