package block

import "mazzy/kmazarin/device/virtio/irq"

// configureBlockInterrupt configures MSI-X for the VirtIO block device on AMD64.
//
// On QEMU q35, PCI INTx routes through the ICH9 PIRQ router to IOAPIC inputs
// 16-19, which fall outside our ISR stub range (vectors 32-47). MSI-X bypasses
// the IOAPIC entirely — the device writes directly to the LAPIC with a vector
// we choose in the 32-47 range, matching the input devices' approach.
//
// Returns the IRQ number (vector - 32) for NonTimerIRQTopHalf, or 0 on failure.
func configureBlockInterrupt(bus, slot, funcNum uint8) uint32 {
	return irq.ConfigureMSIXForDevice(bus, slot, funcNum)
}

// blockMSIXVector returns the MSI-X vector to assign to the VirtIO config and
// queue registers during the handshake. On AMD64, we use MSI-X vector 0.
func blockMSIXVector() uint16 {
	return 0
}
