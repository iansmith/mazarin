package input

// platformInitInterrupts initializes platform-specific interrupt infrastructure.
// ARM64: sets up GICv2m SPI base for MSI-X routing.
func platformInitInterrupts() {
	initGICv2mSPIBase()
}

// platformConfigureDeviceIRQ configures interrupts for a VirtIO input device.
// ARM64: programs MSI-X table entries to target GICv2m doorbell.
// Returns the GIC IRQ number for this device, or 0 on failure.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	return configureMSIX(bus, slot, funcNum)
}

// ConfigureMSIXForDevice is a public wrapper around configureMSIX so other
// VirtIO drivers (block, GPU) can reuse the MSI-X setup.
// Returns the GIC IRQ number (SPI + 32) for this device, or 0 on failure.
func ConfigureMSIXForDevice(bus, slot, funcNum uint8) uint32 {
	return configureMSIX(bus, slot, funcNum)
}
