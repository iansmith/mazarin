//go:build amd64

package main

import (
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/deviceapi"
)

// cachedIOAPIC holds the IOAPIC interrupt controller reference for x86_64.
// Used only for COM1 UART (ISA IRQ 4). The LAPIC timer is local and doesn't
// need the IOAPIC, and VirtIO devices use MSI-X which bypasses the IOAPIC.
var cachedIOAPIC deviceapi.InterruptController

// initInterruptController discovers and caches the IOAPIC for later use.
func initInterruptController() {
	if ic, ok := device.GetInterruptController(); ok {
		cachedIOAPIC = ic
	}
}

// enableTimerAtController is a no-op on x86_64.
// The LAPIC timer is local to the CPU and doesn't route through the IOAPIC.
func enableTimerAtController() {}

// disableTimerAtController is a no-op on x86_64.
func disableTimerAtController() {}

// enableDeviceIRQ is a no-op on x86_64.
// VirtIO devices use MSI-X which writes directly to the LAPIC,
// bypassing the IOAPIC entirely.
func enableDeviceIRQ(irq uint32, isrBase uintptr) {}

// enableBlockDeviceIRQ is a no-op on x86_64.
// Block device uses MSI-X which bypasses the IOAPIC.
func enableBlockDeviceIRQ(irq uint32) {}

// enableIOAPICIRQ enables an IRQ at the IOAPIC. Used for legacy ISA devices
// like COM1 UART that route through the IOAPIC rather than MSI-X.
func enableIOAPICIRQ(irq uint32) {
	if cachedIOAPIC != nil {
		cachedIOAPIC.EnableIRQ(irq)
	}
}
