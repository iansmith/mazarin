package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
)

// unmapCardinal is a no-op on RISC-V.
// On ARM64, Cardinal occupies low memory mapped via TTBR0 which must be unmapped.
// On RISC-V, there's a single SATP page table root (no TTBR0/TTBR1 split),
// so there's nothing to unmap.
//
//go:nosplit
func unmapCardinal() {
	// No-op on RISC-V
}

// EarlyInit initializes the early console and thread management for RISC-V.
// Uses NS16550 UART at 0x10000000 (QEMU virt machine).
//
// CRITICAL: When booting directly (not via diplomat), OpenSBI loads us with
// SATP=0 (bare mode, no paging). We must use physical addresses until page
// tables are set up. The UART is at physical address 0x10000000.
func EarlyInit() {
	// Set up early console using NS16550 UART (MMIO-based)
	// Use physical address (0x10000000) since paging is not enabled yet.
	// OpenSBI's PMP allows S-mode access to this range.
	const uartBase = uintptr(0x10000000)
	earlyConsole := console.NewMMIOUartConsole(uartBase)
	console.Set(earlyConsole)

	// Initialize thread management system
	InitThreads()

	// Register all device drivers (no hardware access yet)
	device.RegisterAllDrivers()
}
