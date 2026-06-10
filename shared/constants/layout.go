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
	DtbSize                = 0x100000 // 1 MB - Device Tree Blob
	CardinalAllocationSize = 0xF00000 // 15 MB - Cardinal code+data+bss+heap
	// FramebufferSize is a STATIC LAYOUT RESERVATION (load-bearing): it shifts
	// PageTableStart/KmazarinLoadAddr, i.e. the kernel's physical load address.
	// It is NOT the GPU framebuffer allocation cap — that is framebuffer_max_mb in
	// the kernel TOML (see KernelConfig / gpu.SetFramebufferMaxBytes). Changing
	// this value moves the kernel load address (a coordinated both-arch rebuild).
	FramebufferSize = 0x4000000 // 64 MB reserved before the kernel in the layout
	PageTableSize   = 0x800000  // 8 MB - ARM64 page tables (L0/L1/L2/L3)
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

	// Kmazarin load address (immediately after page tables)
	KmazarinLoadAddr = PageTableEnd
)

// ============================================================================
// Stack Layout
// ============================================================================
// Stacks are located at high memory, below the 1GB boundary.
//
// Layout:
//   0x5EFF0000 - 0x5F000000  (64 KB)  g0 stack (SP_EL0, normal kernel execution)
//   0x5F000000 - 0x5F020000  (128 KB) Exception stack (SP_EL1, IRQ/FIQ/exceptions)

const (
	G0StackBottom      = 0x5EFF0000 // Bottom of g0 stack
	G0StackTop         = 0x5F000000 // Top of g0 stack (SP_EL0)
	ExceptionStackTop  = 0x5F020000 // Top of exception stack (SP_EL1)
	ExceptionStackSize = 0x20000    // 128 KB
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

	// PCI BAR allocation pool
	// Used for VirtIO devices and other PCI peripherals
	PciBarBase = 0x11000000
	PciBarSize = 0x0F000000 // 240 MB
)

// ============================================================================
// Helper Functions
// ============================================================================

// MemoryLayoutSummary returns a human-readable summary of the memory layout.
// NOTE: Addresses are computed from constants - see actual values in the constants above.
// Useful for debugging and documentation.
func MemoryLayoutSummary() string {
	return `Cardinal Memory Layout (addresses computed from constants):
  RAM Start:       0x40000000
  DTB:             1 MB
  Cardinal:        15 MB
  Framebuffer:     64 MB (VirtIO GPU)
  Page Tables:     8 MB
  Kmazarin:        After page tables
  g0 Stack:        64 KB (SP_EL0)
  Exception Stack: 128 KB (SP_EL1)

MMIO Devices (QEMU virt):
  GIC:             0x08000000 (128 KB)
  UART:            0x09000000 (64 KB)
  RTC:             0x09010000
  fw_cfg:          0x09020000 (64 KB)
  bochs-display:   0x10000000 (16 MB)
  PCI BARs:        0x11000000 (240 MB)
`
}
