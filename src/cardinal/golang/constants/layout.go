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

	// Kmazarin load address (immediately after page tables)
	KmazarinLoadAddr = PageTableEnd // = 0x41800000
)

// ============================================================================
// Stack Layout
// ============================================================================
// Stacks are located at high memory, below the 1GB boundary.
//
// Layout (Low Memory - Cardinal bootstrap only):
//   0x5EFF0000 - 0x5F000000  (64 KB)  g0 stack (SP_EL0, normal kernel execution)
//   0x5F000000 - 0x5F020000  (128 KB) Exception stack (SP_EL1, IRQ/FIQ/exceptions)
//
// Layout (High Memory - Kmazarin kernel, TTBR1):
//   0xFFFFFFFF5EFF8000 - 0xFFFFFFFF5F000000  (16 KB)  Kernel g0 stack
//   0xFFFFFFFF5F000000 - 0xFFFFFFFF5F002000  (8 KB)   Kernel exception stack

const (
	// Low-memory stacks (Cardinal bootstrap, TTBR0)
	G0StackBottom      = 0x5EFF0000 // Bottom of g0 stack
	G0StackTop         = 0x5F000000 // Top of g0 stack (SP_EL0)
	ExceptionStackTop  = 0x5F020000 // Top of exception stack (SP_EL1)
	ExceptionStackSize = 0x20000    // 128 KB

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
// Helper Functions
// ============================================================================

// MemoryLayoutSummary returns a human-readable summary of the memory layout.
// Useful for debugging and documentation.
func MemoryLayoutSummary() string {
	return `Cardinal Memory Layout:
  RAM Start:       0x40000000
  DTB:             0x40000000 - 0x40100000 (1 MB)
  Cardinal:        0x40100000 - 0x41000000 (15 MB)
  Page Tables:     0x41000000 - 0x41800000 (8 MB)
  Kmazarin:        0x41800000+
  g0 Stack:        0x5EFF0000 - 0x5F000000 (64 KB, SP_EL0)
  Exception Stack: 0x5F000000 - 0x5F020000 (128 KB, SP_EL1)

MMIO Devices (QEMU virt):
  GIC:             0x08000000 - 0x08020000 (128 KB)
  UART:            0x09000000 - 0x09010000 (64 KB)
  RTC:             0x09010000
  fw_cfg:          0x09020000 - 0x09030000 (64 KB)
  bochs-display:   0x10000000 - 0x11000000 (16 MB)
  PCI BARs:        0x11000000 - 0x20000000 (240 MB)
`
}
