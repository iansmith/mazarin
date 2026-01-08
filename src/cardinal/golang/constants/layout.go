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

	// Page table region
	PageTableStart = CardinalEnd
	PageTableEnd   = PageTableStart + PageTableSize

	// Kmazarin load address (low memory, for ELF build with -T flag)
	// The ELF is built with this address, but we map it to high memory at runtime
	KmazarinLoadAddr = PageTableEnd // = 0x41800000

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
// Stacks are located at high memory, below the 1GB boundary.
//
// Layout (Low Memory - Cardinal bootstrap only):
//   0x5EFF8000 - 0x5F000000  (32 KB)  g0 stack (SP_EL0, normal kernel execution)
//   0x5F000000 - 0x5F004000  (16 KB)  Exception stack (SP_EL1, IRQ/FIQ/exceptions)
//
// Layout (High Memory - Kmazarin kernel, TTBR1):
//   0xFFFFFFFF5EFFC000 - 0xFFFFFFFF5F000000  (16 KB)  Kernel g0 stack
//   0xFFFFFFFF5F000000 - 0xFFFFFFFF5F002000  (8 KB)   Kernel exception stack

const (
	// Low-memory stacks (Cardinal bootstrap, TTBR0)
	G0StackBottom      = 0x5EFF8000 // Bottom of g0 stack (32 KB)
	G0StackTop         = 0x5F000000 // Top of g0 stack (SP_EL0)
	ExceptionStackTop  = 0x5F004000 // Top of exception stack (SP_EL1)
	ExceptionStackSize = 0x4000     // 16 KB

	// High-memory stacks (Kmazarin kernel, TTBR1)
	// Stack sizes tuned for tail-call optimization pattern:
	// - ABI stubs use JMP (tail-call), not CALL - no stack frame added
	// - Exception handlers save ~256 bytes + small working space
	// - Go runtime init needs ~8KB peak during schedinit
	KernelG0StackSize     = 0x4000                             // 16 KB
	KernelG0StackTop      = 0xFFFFFFFF5F000000                 // Top of kernel g0 stack
	KernelG0StackBottom   = KernelG0StackTop - KernelG0StackSize

	KernelExcStackSize    = 0x2000                             // 8 KB
	KernelExcStackTop     = 0xFFFFFFFF5F002000                 // Top of kernel exception stack
	KernelExcStackBottom  = KernelExcStackTop - KernelExcStackSize
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
	KernelTextBase = KernelVAOffset + KmazarinLoadAddr // 0xFFFFFFFF41800000

	// DTB mapped to high memory (read-only kernel copy)
	KernelDtbBase = KernelVAOffset + DtbStart // 0xFFFFFFFF40000000

	// =========================================================================
	// Kmazarin Demand Paging Memory Layout (64MB total kernel footprint)
	// =========================================================================
	// Memory layout within 64MB kernel space:
	//   0xFFFFFFFF41800000 - Kmazarin static (code/data/bss) ~2MB
	//   0xFFFFFFFF419C0000 - TTBR1 PT Region (mapped page tables) 64KB
	//   0xFFFFFFFF419D0000 - PT Pool (for L2/L3 allocation) 192KB
	//   0xFFFFFFFF41A00000 - Heap VA space (demand-paged) ~62MB
	//   0xFFFFFFFF45800000 - End of 64MB kernel

	// TTBR1 page table region - Cardinal's TTBR1 L0/L1/L2 tables mapped here
	// so kmazarin can modify them for demand paging
	KernelTTBR1RegionVA   = 0xFFFFFFFF419C0000 // Where TTBR1 tables are mapped
	KernelTTBR1RegionSize = 0x10000            // 64KB (16 page tables max)

	// PT Pool - kmazarin allocates new L2/L3 tables from here
	KernelPTPoolStart = 0xFFFFFFFF419D0000 // Start of PT pool
	KernelPTPoolEnd   = 0xFFFFFFFF41A00000 // End of PT pool (192KB)
	KernelPTPoolSize  = KernelPTPoolEnd - KernelPTPoolStart

	// Heap VA space - demand-paged, backed by frame pool
	KernelHeapStart = 0xFFFFFFFF41A00000 // Start of heap VA space
	KernelHeapEnd   = 0xFFFFFFFF45800000 // End of 64MB kernel
	KernelHeapSize  = KernelHeapEnd - KernelHeapStart

	// Kernel frame pool - backing pages for kmazarin heap demand paging
	// Physical memory after kmazarin static region, used to back heap pages
	// NOTE: This is the KERNEL frame pool. User-space processes will have
	// their own separate frame pool (to be defined later).
	KernelFramePoolPhysStart = 0x41A00000 // Physical start of kernel frame pool
	KernelFramePoolPhysEnd   = 0x45800000 // Physical end (64MB from kernel start)
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
  Page Tables:     0x41000000 - 0x41800000 (8 MB, TTBR0)
  Kmazarin:        0x41800000 - ~0x42000000 (physical, ~8MB max)
  g0 Stack:        0x5EFF8000 - 0x5F000000 (32 KB, SP_EL0)
  Exception Stack: 0x5F000000 - 0x5F004000 (16 KB, SP_EL1)

Kmazarin Kernel Layout (High Memory, TTBR1):
  DTB:             0xFFFFFFFF40000000 - 0xFFFFFFFF40100000 (1 MB, read-only)
  Kernel Text:     0xFFFFFFFF41800000 - ~0xFFFFFFFF42000000 (8 MB max)
  Kernel Heap:     0xFFFFFFFF4A000000+ (64 MB limit total)
  g0 Stack:        0xFFFFFFFF5EFFC000 - 0xFFFFFFFF5F000000 (16 KB)
  Exception Stack: 0xFFFFFFFF5F000000 - 0xFFFFFFFF5F002000 (8 KB)
  Page Tables:     0xFFFFFFFF60000000 - 0xFFFFFFFF60800000 (8 MB, TTBR1)

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
