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
	FramebufferSize        = 0x800000  // 8 MB - VirtIO GPU framebuffer
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
	FramebufferPhysAddr = CardinalEnd      // = 0x41000000
	FramebufferEnd      = FramebufferPhysAddr + FramebufferSize

	// Page table region (shifted after framebuffer)
	PageTableStart = FramebufferEnd        // = 0x41800000
	PageTableEnd   = PageTableStart + PageTableSize

	// Kmazarin load address (low memory, for ELF build with -T flag)
	// The ELF is built with this address, but we map it to high memory at runtime
	KmazarinLoadAddr = PageTableEnd        // = 0x42000000

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

	// Physical base address for kernel stacks region
	// Placed at offset from RAM start, chosen to avoid conflicts with:
	// - DTB (1MB), Cardinal (15MB), Page Tables (8MB), Kmazarin (up to 64MB)
	// This gives us ~480MB from RAM start = plenty of safety margin
	KernelStacksPhysOffset = 0x1EFF8000                              // ~494MB from RAM start
	KernelStacksPhysBase   = BootAddress + KernelStacksPhysOffset    // 0x5EFF8000
	KernelStacksVirtBase   = KernelVAOffset + KernelStacksPhysBase   // 0xFFFFFFFF5EFF8000

	// Low-memory stacks (Cardinal bootstrap, TTBR0) - computed from base
	G0StackBottom      = KernelStacksPhysBase                        // Bottom of g0 stack
	G0StackTop         = G0StackBottom + KernelG0StackSize           // Top of g0 stack (SP_EL0)
	ExceptionStackTop  = G0StackTop + KernelExcStackSize             // Top of exception stack (SP_EL1)
	ExceptionStackSize = KernelExcStackSize                          // Exception stack size

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
	// Memory layout:
	//   0xFFFFFFFF42000000 - Kmazarin static (code/data/bss) ~2MB
	//   0xFFFFFFFF421C0000 - TTBR1 PT Region (mapped page tables) 64KB
	//   0xFFFFFFFF421D0000 - PT Pool (for L2/L3 allocation) 192KB
	//   0xFFFFFFFF42200000 - Heap VA space (demand-paged) ~1GB
	//   0xFFFFFFFF80000000 - End of heap VA space

	// TTBR1 page table region - Cardinal's TTBR1 L0/L1/L2 tables mapped here
	// so kmazarin can modify them for demand paging
	KernelTTBR1RegionVA   = 0xFFFFFFFF421C0000 // Where TTBR1 tables are mapped
	KernelTTBR1RegionSize = 0x10000            // 64KB (16 page tables max)

	// PT Pool - kmazarin allocates new L2/L3 tables from here
	KernelPTPoolStart = 0xFFFFFFFF421D0000 // Start of PT pool
	KernelPTPoolEnd   = 0xFFFFFFFF42200000 // End of PT pool (192KB)
	KernelPTPoolSize  = KernelPTPoolEnd - KernelPTPoolStart

	// Heap VA space - demand-paged, backed by frame pool
	// Go wants to reserve huge contiguous VA regions (many GB) for arena metadata.
	// VA space is free - only accessed pages consume physical frames.
	// With arenaBaseOffset = 0xFFFF000000000000, we can use up to 128TB of VA space.
	// The arena index formula: (addr - offset) >> 26 must be < 2^22
	KernelHeapStart = 0xFFFF000100000000 // Start in TTBR1 space (256GB from base)
	KernelHeapEnd   = 0xFFFF100000000000 // End of heap VA space (16TB range)
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart

	// Kernel frame pool - backing pages for kmazarin heap demand paging
	// Physical memory after kmazarin static region, used to back heap pages
	// NOTE: This is the KERNEL frame pool. User-space processes will have
	// their own separate frame pool (to be defined later).
	KernelFramePoolPhysStart = 0x42200000 // Physical start of kernel frame pool
	KernelFramePoolPhysEnd   = 0x80000000 // Physical end (~1GB, end of RAM)
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
)

// ============================================================================
// Helper Functions
// ============================================================================

// MemoryLayoutSummary returns a human-readable summary of the memory layout.
// Useful for debugging and documentation.
func MemoryLayoutSummary() string {
	return `Cardinal Memory Layout (Low Memory, TTBR0):
  RAM Start:       0x40000000
  DTB:             0x40000000 - 0x40100000 (1 MB)
  Cardinal:        0x40100000 - 0x41000000 (15 MB)
  Framebuffer:     0x41000000 - 0x41800000 (8 MB, VirtIO GPU)
  Page Tables:     0x41800000 - 0x42000000 (8 MB, TTBR0)
  Kmazarin:        0x42000000 - ~0x42200000 (physical, ~8MB max)
  g0 Stack:        Computed: KernelStacksPhysBase + KernelG0StackSize (32 KB, SP_EL0)
  Exception Stack: Computed: G0StackTop + KernelExcStackSize (16 KB, SP_EL1)

Kmazarin Kernel Layout (High Memory, TTBR1):
  DTB:             Computed: DtbPhysAddr + KernelVAOffset (1 MB, read-only)
  Kernel Text:     Computed: KmazarinLoadAddr + KernelVAOffset (8 MB max)
  Kernel Heap:     Computed from KernelHeapStart to KernelHeapEnd (demand-paged)
  g0 Stack:        Computed: KernelStacksVirtBase to KernelG0StackTop (32 KB)
  Exception Stack: Computed: KernelG0StackTop to KernelExcStackTop (16 KB)
  Page Tables:     Identity-mapped via setupKernelPageTables() (TTBR1)

MMIO Devices (Physical):
  GIC:             0x08000000 - 0x08020000 (128 KB)
  UART:            0x09000000 - 0x09010000 (64 KB)
  RTC:             0x09010000
  fw_cfg:          0x09020000 - 0x09030000 (64 KB)
  bochs-display:   0x10000000 - 0x11000000 (16 MB)
  PCI BARs:        0x11000000 - 0x20000000 (240 MB)

MMIO Devices (Kernel High Memory, TTBR1):
  GIC:             0xFFFFFFFF08000000 - 0xFFFFFFFF08020000
  UART:            0xFFFFFFFF09000000 - 0xFFFFFFFF09010000
`
}
