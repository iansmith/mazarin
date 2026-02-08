//go:build amd64 && !test_stubs

package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
)

// unmapCardinal is a no-op on x86_64.
// On ARM64, Cardinal occupies low memory mapped via TTBR0 which must be unmapped.
// On x86_64, diplomat grafts kernel mappings into the UEFI PML4 — there's no
// separate TTBR0/TTBR1 split, so there's nothing to unmap.
//
//go:nosplit
func unmapCardinal() {
	// No-op on x86_64
}

// EarlyInit initializes the early console and thread management for x86_64.
// Uses COM1Console (I/O port 0x3F8) instead of ARM64's MMIO PL011.
func EarlyInit() {
	// Set up early console using COM1 serial port
	earlyConsole := console.NewCOM1Console()
	console.Set(earlyConsole)

	// Initialize thread management system
	InitThreads()

	// Register all device drivers (no hardware access yet)
	device.RegisterAllDrivers()
}
