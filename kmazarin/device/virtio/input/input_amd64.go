package input

import (
	"mazzy/kmazarin/device/virtio/irq"
)

// platformInitInterrupts is a no-op on x86_64.
func platformInitInterrupts() {}

// platformConfigureDeviceIRQ configures MSI-X for a VirtIO device on x86_64.
//
// On x86, MSI-X writes directly to the LAPIC (0xFEE00000) with a vector number
// in the data field. This bypasses the IOAPIC entirely. The CPU receives the
// vector as if it came from the IOAPIC, and our isrDev32-47 stubs handle it.
//
// Returns the IRQ number (vector - 32) for use by NonTimerIRQTopHalf, or 0 on failure.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	return irq.ConfigureMSIXForDevice(bus, slot, funcNum)
}

// platformMSIXVector returns 0 on x86_64 (MSI-X vector 0).
// x86_64 uses MSI-X, so the device should use vector 0 for notifications.
func platformMSIXVector() uint16 {
	return 0
}
