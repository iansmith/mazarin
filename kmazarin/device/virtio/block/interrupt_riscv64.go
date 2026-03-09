package block

import "mazzy/kmazarin/pci"

// QEMU virt machine routes PCI INTx to PLIC sources 32-35.
// Swizzle: plic_source = 32 + (slot + pin - 1) % 4
const plicPCIINTxBase = 32

// configureBlockInterrupt computes the PLIC source number for the block
// device's PCI INTx interrupt. Same swizzle formula as input devices.
func configureBlockInterrupt(bus, slot, funcNum uint8) uint32 {
	pin := pci.ConfigRead8(bus, slot, funcNum, 0x3D) // PCI Interrupt Pin
	if pin == 0 || pin > 4 {
		return 0
	}
	return plicPCIINTxBase + (uint32(slot)+uint32(pin)-1)%4
}

// blockMSIXVector returns 0xFFFF (VIRTIO_MSI_NO_VECTOR) on RISC-V.
// RISC-V uses INTx (PCI legacy interrupts through PLIC), not MSI-X.
func blockMSIXVector() uint16 {
	return 0xFFFF
}
