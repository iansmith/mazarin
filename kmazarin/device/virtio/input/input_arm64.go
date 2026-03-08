package input

import (
	"mazzy/kmazarin/pci"
)

// platformInitInterrupts is a no-op on ARM64.
// PCI INTx interrupts are routed through the GIC; no MSI-X setup needed.
func platformInitInterrupts() {}

// platformConfigureDeviceIRQ computes the GIC IRQ number for a PCI device's
// INTx interrupt, and ensures MSI-X is disabled at the PCI capability level
// so INTx delivery works. UEFI may have enabled MSI-X during boot.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	// Disable MSI-X at the PCI capability level. When MSI-X is enabled,
	// PCI spec says INTx is automatically disabled. UEFI firmware may
	// have enabled MSI-X during device enumeration.
	disableMSIX(bus, slot, funcNum)

	pin := pci.ConfigRead8(bus, slot, funcNum, PCI_INTERRUPT_PIN)
	return computeIRQ(slot, pin)
}

// disableMSIX finds the MSI-X capability and clears the enable bit.
func disableMSIX(bus, slot, funcNum uint8) {
	capPtr := pci.ConfigRead8(bus, slot, funcNum, pci.PCI_CAPABILITIES)
	for i := 0; i < 32 && capPtr != 0 && capPtr != 0xFF; i++ {
		capID := pci.ConfigRead8(bus, slot, funcNum, capPtr)
		if capID == PCI_CAP_MSIX {
			// Read Message Control (upper 16 bits of the capability dword)
			capDword := pci.ConfigRead32(bus, slot, funcNum, capPtr)
			// Clear bit 15 (MSI-X Enable) and bit 14 (Function Mask)
			msgCtrl := uint16(capDword >> 16)
			msgCtrl &^= (1 << 15) | (1 << 14)
			pci.ConfigWrite32(bus, slot, funcNum, capPtr, (capDword&0xFFFF)|uint32(msgCtrl)<<16)
			return
		}
		capPtr = pci.ConfigRead8(bus, slot, funcNum, capPtr+1)
	}
}

// ConfigureMSIXForDevice is a no-op on ARM64. ARM64 uses PCI INTx
// routing, not MSI-X. Returns 0 (no MSI-X IRQ).
func ConfigureMSIXForDevice(bus, slot, funcNum uint8) uint32 {
	return 0
}

// platformMSIXVector returns 0xFFFF (VIRTIO_MSI_NO_VECTOR) on ARM64.
// Signals to initDevice to skip MSI-X config writes.
func platformMSIXVector() uint16 {
	return 0xFFFF
}
