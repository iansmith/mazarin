// layout.go - Memory Layout Constants for Cardinal/Kmazarin
//
// SINGLE SOURCE OF TRUTH for all memory layout configuration.
//
// These constants define the physical memory layout for the Cardinal bootloader
// and Kmazarin kernel on QEMU's virt machine (ARM64).
//
// CRITICAL: These values must match across:
//   - Cardinal bootloader (this file)
//   - Kmazarin kernel build (via tools/kmazarin-entry.sh)
//   - Assembly boot code (via main.Linker* symbols)
//   - Post-build patching (via compute-linker-values.go)
//
// To change the memory layout, modify ONLY this file.

package constants

// ============================================================================
// Base Memory Layout - QEMU virt Machine
// ============================================================================

const (
	// Physical RAM starts at 0x40000000 on QEMU virt (ARM64)
	BootAddress = 0x40000000

	// Allocation sizes for each component
	DtbSize                = 0x100000  // 1 MB - Device Tree Blob
	CardinalAllocationSize = 0xF00000  // 15 MB - Cardinal code+data+bss+heap
	FramebufferSize        = 0x2000000 // 32 MB - VirtIO GPU framebuffer (supports up to 4K)
	PageTableSize          = 0x800000  // 8 MB - ARM64 page tables (L0/L1/L2/L3)
)

// ============================================================================
// Calculated Memory Boundaries
// ============================================================================
// These are computed from the base constants above.
// DO NOT modify these directly - change the constants above instead.

const (
	// DTB (Device Tree Blob) region
	DtbStart = BootAddress
	DtbEnd   = DtbStart + DtbSize

	// Cardinal region
	CardinalStart = DtbEnd
	CardinalEnd   = CardinalStart + CardinalAllocationSize

	// VirtIO GPU framebuffer region
	FramebufferPhysAddr = CardinalEnd
	FramebufferEnd      = FramebufferPhysAddr + FramebufferSize

	// Page table region (shifted after framebuffer)
	PageTableStart = FramebufferEnd
	PageTableEnd   = PageTableStart + PageTableSize

	// Kmazarin load address (low memory, for ELF build with -T flag)
	// The ELF is built with this address, but we map it to high memory at runtime
	KmazarinLoadAddr = PageTableEnd

	// Kernel VA offset - add this to physical addresses to get high-memory kernel VAs
	// Physical 0x40000000 -> Kernel VA 0xFFFFFFFF40000000
	KernelVAOffset = 0xFFFFFFFF00000000

	// Kmazarin kernel virtual base address (high memory, TTBR1)
	KernelVABase = KernelVAOffset + BootAddress // 0xFFFFFFFF40000000

	// Kmazarin size limits (enforced at boot and runtime)
	KmazarinBinaryMaxSize = 0x800000       // 8 MB max for kmazarin binary (enforced at boot)
	KmazarinTotalLimit    = 64 * 1024 * 1024 // 64 MB for binary + heap combined
)

// ============================================================================
// Stack Layout
// ============================================================================
// Stacks are located at high memory, well above Cardinal and kmazarin regions.
// All stack addresses are COMPUTED from a base physical address - no hardcoded addresses.

const (
	// Stack sizes (these are configuration, not addresses)
	// DOUBLED from 16KB/8KB to test if stack overflow is causing x28 corruption
	KernelG0StackSize  = 0x8000 // 32 KB (doubled from 16KB)
	KernelExcStackSize = 0x4000 // 16 KB (doubled from 8KB)

	// Physical base address for Cardinal's bootstrap stacks (low memory, TTBR0)
	// These are used before MMU is enabled, so they must be in valid physical RAM.
	KernelStacksPhysOffset = 0x1EFF8000                              // ~494MB from RAM start
	KernelStacksPhysBase   = BootAddress + KernelStacksPhysOffset    // 0x5EFF8000

	// Low-memory stacks (Cardinal bootstrap, TTBR0) - computed from base
	G0StackBottom      = KernelStacksPhysBase                        // Bottom of g0 stack
	G0StackTop         = G0StackBottom + KernelG0StackSize           // Top of g0 stack (SP_EL0)
	ExceptionStackTop  = G0StackTop + KernelExcStackSize             // Top of exception stack (SP_EL1)
	ExceptionStackSize = KernelExcStackSize                          // Exception stack size

	// High-memory kernel stack virtual addresses (TTBR1)
	// These must NOT fall within any 2MB block covered by createLinearMap(),
	// otherwise the 4KB page table entries create a gap in the linear map.
	// The linear map covers PA from unifiedPoolStart (~0x44200000) to ramEnd.
	// We place stacks in the gap between the PT identity map end (0x43800000)
	// and the linear map start, using L1=509 (same as kmazarin text).
	KernelStacksVirtBase = KernelVAOffset + 0x43E00000               // 0xFFFFFFFF43E00000

	// High-memory stacks (Kmazarin kernel, TTBR1) - computed from virtual base
	KernelG0StackBottom  = KernelStacksVirtBase                      // Bottom of kernel g0 stack
	KernelG0StackTop     = KernelG0StackBottom + KernelG0StackSize   // Top of kernel g0 stack (SP_EL0)
	KernelExcStackBottom = KernelG0StackTop                          // Bottom of exception stack
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize // Top of exception stack (SP_EL1)
)

// ============================================================================
// MMIO Device Addresses - QEMU virt Machine Fixed Addresses
// ============================================================================
// These are physical addresses defined by QEMU's virt machine hardware layout.
// They are NOT configurable - QEMU hardcodes these addresses.
//
// Reference: https://github.com/qemu/qemu/blob/master/hw/arm/virt.c

const (
	// GIC (Generic Interrupt Controller) - ARM GICv2
	GicBase = 0x08000000 // GICD at 0x08000000, GICC at 0x08010000
	GicSize = 0x00020000 // 128 KB (distributor + CPU interface)

	// UART PL011 (serial console)
	UartBase = 0x09000000
	UartSize = 0x00010000 // 64 KB

	// RTC PL031 (real-time clock)
	RtcBase = 0x09010000

	// QEMU fw_cfg device (firmware configuration)
	FwcfgBase = 0x09020000
	FwcfgSize = 0x00010000 // 64 KB

	// bochs-display framebuffer (graphics)
	BochsDisplayBase = 0x10000000
	BochsDisplaySize = 0x01000000 // 16 MB

	// PCI BAR allocation pool
	// Used for VirtIO devices and other PCI peripherals
	PciBarBase = 0x11000000
	PciBarSize = 0x0F000000 // 240 MB
)

// ============================================================================
// High-Memory Kernel Addresses (TTBR1)
// ============================================================================
// These addresses are used by kmazarin when running at high memory.
// They map to the same physical devices as the low-memory addresses above,
// but through TTBR1 for kernel-space access.

const (
	// High-memory MMIO offset - add this to physical MMIO addresses
	// Physical 0x08000000 -> Kernel MMIO VA 0xFFFFFFFF08000000
	KernelMMIOOffset = 0xFFFFFFFF00000000

	// High-memory MMIO device addresses (kernel access via TTBR1)
	KernelUartBase = KernelMMIOOffset + UartBase // 0xFFFFFFFF09000000
	KernelGicBase  = KernelMMIOOffset + GicBase  // 0xFFFFFFFF08000000

	// Kmazarin memory regions (high memory, TTBR1)
	// These are virtual addresses where kmazarin code, heap, and page tables live
	KernelTextBase = KernelVAOffset + KmazarinLoadAddr // 0xFFFFFFFF42000000

	// DTB mapped to high memory (read-only kernel copy)
	KernelDtbBase = KernelVAOffset + DtbStart // 0xFFFFFFFF40000000

	// =========================================================================
	// Kmazarin Demand Paging Memory Layout (64MB total kernel footprint)
	// =========================================================================
	// Memory layout (COMPUTED relative to KernelTextBase):
	//   KernelTextBase+0x000000 - Kmazarin static (code/data/bss) ~2MB
	//   KernelTextBase+0x1C0000 - TTBR1 PT Region (mapped page tables) 64KB
	//   KernelTextBase+0x1D0000 - PT Pool (for L2/L3 allocation) 512KB
	//   0xFFFF000100000000 - Heap VA space (demand-paged) ~16TB
	//
	// All addresses except heap are computed from KernelTextBase so they
	// automatically adjust when the memory layout changes (e.g., framebuffer size).

	// Size constants (fixed)
	KernelTTBR1RegionSize = 0x10000   // 64KB (16 page tables max)
	KernelPTPoolSize      = 0x400000  // 4MB (1024 pages) - supports multiple userspace processes

	// TTBR1 page table region - Cardinal's TTBR1 L0/L1/L2 tables mapped here
	// so kmazarin can modify them for demand paging.
	//
	// NOTE: These constants are DEPRECATED - the actual values are computed
	// dynamically at runtime from LinkerKmazarinSize in initComputedMemoryLayout()
	// (see mmu_constants.go). The computed values are placed immediately after
	// kmazarin's static regions (page-aligned) with zero wasted space.
	//
	// These constants remain for documentation and as worst-case estimates.
	KernelTTBR1RegionVA = KernelTextBase + 0x200000 // DEPRECATED: Computed at runtime

	// PT Pool - kmazarin allocates new L2/L3 tables from here
	// NOTE: DEPRECATED - computed at runtime from LinkerKmazarinSize
	KernelPTPoolStart = KernelTTBR1RegionVA + KernelTTBR1RegionSize // DEPRECATED
	KernelPTPoolEnd   = KernelPTPoolStart + KernelPTPoolSize        // DEPRECATED

	// Heap VA space - demand-paged, backed by frame pool
	// Go wants to reserve huge contiguous VA regions (many GB) for arena metadata.
	// VA space is free - only accessed pages consume physical frames.
	// With arenaBaseOffset = 0xFFFF000000000000, we can use up to 128TB of VA space.
	// The arena index formula: (addr - offset) >> 26 must be < 2^22
	KernelHeapStart = 0xFFFF000100000000 // Start in TTBR1 space (256GB from base)
	KernelHeapEnd   = 0xFFFF100000000000 // End of heap VA space (16TB range)
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart

	// Kernel frame pool - backing pages for kmazarin heap demand paging
	// Physical memory after PT pool, used to back heap pages
	// NOTE: This is the KERNEL frame pool. User-space processes will have
	// their own separate frame pool (to be defined later).
	//
	// NOTE: This constant is DEPRECATED - the actual start is computed
	// dynamically at runtime based on computedPTPoolEnd (see mmu_constants.go).
	// The value here is a worst-case estimate for documentation.
	KernelFramePoolPhysStart = KmazarinLoadAddr + 0x290000 // DEPRECATED: Computed at runtime
	KernelFramePoolPhysEnd   = 0x80000000                  // Physical end (~1GB, end of RAM)
	KernelFramePoolSize      = KernelFramePoolPhysEnd - KernelFramePoolPhysStart

	// Legacy constant for compatibility (now equals KernelTTBR1RegionVA)
	KernelPageTableBase = KernelTTBR1RegionVA
	KernelPageTableSize = 0x800000 // 8 MB (unused, kept for compatibility)
)

// ============================================================================
// Auxiliary Vector (auxv) Custom Types
// ============================================================================
// Custom AT values for passing cardinal boot information to kmazarin.
// These are added to the standard Linux auxv entries (AT_PAGESZ, AT_RANDOM, etc.)
// and allow kmazarin to discover runtime configuration without a separate
// parameter buffer.
//
// Standard AT values (from Linux):
//   AT_PAGESZ = 6, AT_HWCAP = 16, AT_RANDOM = 25, etc.
//
// Custom AT values (0x1000+ range to avoid conflicts):

const (
	// Cardinal boot information passed via auxv
	AT_DTB_PHYS         = 0x1000 // Physical address of DTB
	AT_DTB_SIZE         = 0x1001 // Size of DTB in bytes
	AT_KMAZARIN_PHYS    = 0x1002 // Physical base address of kmazarin binary
	AT_KMAZARIN_SIZE    = 0x1003 // Total size of kmazarin binary in bytes
	AT_FRAME_POOL_START = 0x1004 // Start of physical frame pool
	AT_FRAME_POOL_END   = 0x1005 // End of physical frame pool
	AT_KERNEL_UART_BASE = 0x1006 // High-memory UART base (0xFFFFFFFF09000000)
	AT_KERNEL_GIC_BASE  = 0x1007 // High-memory GIC base (0xFFFFFFFF08000000)
	AT_TTBR1_L0_PHYS    = 0x1008 // Physical address of TTBR1 L0 page table
	AT_STARTUP_PARAMS   = 0x1009 // High-memory address of kmazarin StartupParams BSS array
	AT_FRAMEBUFFER_PHYS = 0x100A // Physical address of VirtIO GPU framebuffer
	AT_FRAMEBUFFER_SIZE = 0x100B // Size of framebuffer in bytes

	// Kernel heap bounds - parsed by runtime overlay before first mmap
	AT_KMAZARIN_HEAP_START = 0x1010 // Start of kernel heap VA space
	AT_KMAZARIN_HEAP_END   = 0x1011 // End of kernel heap VA space
)

// ============================================================================
// Helper Functions
// ============================================================================

// MemoryLayoutSummary returns a human-readable summary of the memory layout.
// NOTE: Addresses are computed from constants - actual values may differ from examples.
// Useful for debugging and documentation.
func MemoryLayoutSummary() string {
	return `Cardinal Memory Layout (Low Memory, TTBR0):
  RAM Start:       0x40000000
  DTB:             1 MB (from RAM start)
  Cardinal:        15 MB (after DTB)
  Framebuffer:     32 MB (VirtIO GPU, after Cardinal)
  Page Tables:     8 MB (TTBR0, after Framebuffer)
  Kmazarin:        ~8MB max (after Page Tables, physical)
  g0 Stack:        32 KB (SP_EL0, physical, computed from KernelStacksPhysBase)
  Exception Stack: 16 KB (SP_EL1, physical, computed from G0StackTop)

Kmazarin Kernel Layout (High Memory, TTBR1):
  DTB:             1 MB (read-only, computed: DtbPhysAddr + KernelVAOffset)
  Kernel Text:     ~8 MB max (computed: KmazarinLoadAddr + KernelVAOffset)
  Kernel Heap:     Demand-paged (KernelHeapStart to KernelHeapEnd)
  g0 Stack:        32 KB (0xFFFFFFFF43E00000, between PT map and linear map)
  Exception Stack: 16 KB (0xFFFFFFFF43E08000, between PT map and linear map)
  Page Tables:     Identity-mapped via setupKernelPageTables()

MMIO Devices (Physical, fixed by QEMU virt machine):
  GIC:             0x08000000 (128 KB)
  UART:            0x09000000 (64 KB)
  RTC:             0x09010000 (64 KB)
  fw_cfg:          0x09020000 (64 KB)
  VirtIO MMIO:     0x0A000000 (16 KB, up to 16 devices)
  bochs-display:   0x10000000 (16 MB)
  PCI BARs:        0x11000000 (240 MB)

MMIO Devices (Kernel High Memory, TTBR1):
  All devices mapped at 0xFFFFFFFF00000000 + physical address
`
}
