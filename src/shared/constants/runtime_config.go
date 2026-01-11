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
	DtbVirtAddr       uint64 // High-memory virtual address of DTB
	KmazarinPhysAddr  uint64 // Physical base of kmazarin binary
	KmazarinSize      uint64 // Size of kmazarin binary
	FramePoolStart    uint64 // Physical frame pool start
	FramePoolEnd      uint64 // Physical frame pool end
	KernelUartBase    uint64 // High-memory UART VA
	KernelGicBase     uint64 // High-memory GIC VA
	TTBR1L0Phys       uint64 // Physical address of TTBR1 L0
	TTBR0L0Phys       uint64 // Physical address of TTBR0 L0 (for user-space/heap)
	StartupParamsAddr uint64 // Address of StartupParams buffer itself

	// Derived values (computed by Cardinal)
	KernelVAOffset    uint64 // Kernel VA offset - add to physical to get kernel VA
	KernelPTPoolStart uint64 // PT pool start VA
	KernelPTPoolEnd   uint64 // PT pool end VA
	KernelHeapStart   uint64 // Heap start VA
	KernelHeapEnd     uint64 // Heap end VA

	// Standard auxv values
	PageSize uint64 // AT_PAGESZ - typically 4096
	HWCap    uint64 // AT_HWCAP - ARM64 hardware capabilities

	// Stack boundaries and sizes (high-memory kernel stacks, NOT Cardinal bootstrap)
	G0StackBottom        uint64 // Bottom of g0 stack (SP_EL0) - high memory
	G0StackTop           uint64 // Top of g0 stack (SP_EL0) - high memory
	G0StackSize          uint64 // Size of g0 stack
	ExceptionStackBottom uint64 // Bottom of exception stack (SP_EL1) - high memory
	ExceptionStackTop    uint64 // Top of exception stack (SP_EL1) - high memory
	ExceptionStackSize   uint64 // Exception stack size

	// Goroutine struct copy (for unmapping Cardinal's low memory)
	G0StructAddr uint64 // High-memory address where g0 struct should be copied

	// Preemption support
	AsyncPreemptAddr uint64 // High-memory address of runtime.asyncPreempt function
}

// RuntimeConfigMagic is the expected magic value for a valid RuntimeConfig
const RuntimeConfigMagic = 0x4B4D5A52 // "KMZR" in ASCII
