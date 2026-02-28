package block

import (
	"mazzy/kmazarin/pci"
)

// configureBlockInterrupt determines the PCI INTx interrupt number for the
// block device on ARM64. Unlike MSI-X, INTx uses the GIC's pre-wired SPI
// routing and doesn't require any BAR reprogramming or doorbell writes.
//
// Returns the GIC IRQ number or 0 if no interrupt pin is configured.
//
//go:nosplit
func configureBlockInterrupt(bus, slot, funcNum uint8) uint32 {
	// Read PCI Interrupt Pin (config offset 0x3D):
	//   0 = no interrupt, 1 = INTA, 2 = INTB, 3 = INTC, 4 = INTD
	pin := pci.ConfigRead8(bus, slot, funcNum, 0x3D)
	if pin == 0 || pin > 4 {
		return 0
	}

	// QEMU ARM64 virt machine PCI INTx → GIC SPI mapping:
	//   SPI = 3 + ((device_number + pin - 1) % 4)
	//   GIC IRQ = SPI + 32
	// This matches the interrupt-map in QEMU's virt machine DTB.
	spi := 3 + ((uint32(slot) + uint32(pin) - 1) % 4)
	gicIRQ := spi + 32

	return gicIRQ
}
