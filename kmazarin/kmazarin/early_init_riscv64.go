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
// Uses NS16550 UART at VA 0xFFFFFFFF10000000 (QEMU virt machine, linear map).
//
// Diplomat sets up SATP with the linear map before jumping to kmazarin,
// so we always use the linear-mapped VA, not the physical address.
func EarlyInit() {
	// Set up early console using NS16550 UART (MMIO-based)
	// Use linear-mapped VA (diplomat enables SATP before jumping to us).
	const uartBase = uintptr(0xFFFFFFFF10000000)
	earlyConsole := console.NewMMIOUartConsole(uartBase)
	console.Set(earlyConsole)

	// Initialize thread management system
	InitThreads()

	// Register all device drivers (no hardware access yet)
	device.RegisterAllDrivers()
}
