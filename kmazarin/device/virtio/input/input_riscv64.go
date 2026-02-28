package input

import "mazzy/kmazarin/pci"

// platformInitInterrupts is a no-op on RISC-V.
// PCI INTx interrupts are routed through the PLIC; no MSI-X setup needed.
func platformInitInterrupts() {}

// QEMU virt machine routes PCI INTx to PLIC sources 32-35.
// Swizzle: plic_source = 32 + (slot + pin - 1) % 4
const PLIC_PCI_INTX_BASE = 32

// platformConfigureDeviceIRQ computes the PLIC source number for a PCI device's
// INTx interrupt. QEMU's RISC-V virt machine routes PCI INTx pins to PLIC
// sources 32-35 using the standard swizzle formula.
func platformConfigureDeviceIRQ(bus, slot, funcNum uint8) uint32 {
	pin := pci.ConfigRead8(bus, slot, funcNum, PCI_INTERRUPT_PIN)
	if pin == 0 {
		return 0 // No interrupt
	}
	return PLIC_PCI_INTX_BASE + (uint32(slot)+uint32(pin)-1)%4
}

// ConfigureMSIXForDevice is a no-op on RISC-V. RISC-V uses PLIC INTx
// routing, not MSI-X. Returns 0 (no MSI-X IRQ).
func ConfigureMSIXForDevice(bus, slot, funcNum uint8) uint32 {
	return 0
}
