package block

import "mazzy/kmazarin/device/virtio/irq"

// configureBlockInterrupt configures MSI-X for the VirtIO block device on ARM64
// via GICv2m. Programs the MSI-X table to write to the GICv2m SETSPI doorbell,
// allocating a unique SPI for the block device.
//
// Returns the GIC IRQ number (SPI + 32) for NonTimerIRQTopHalf, or 0 on failure.
func configureBlockInterrupt(bus, slot, funcNum uint8) uint32 {
	return irq.ConfigureMSIXForDevice(bus, slot, funcNum)
}

// blockMSIXVector returns the MSI-X vector to assign to the VirtIO config and
// queue registers during the handshake. On ARM64, we use MSI-X vector 0.
func blockMSIXVector() uint16 {
	return 0
}
