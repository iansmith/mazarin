package pci

// pciEcamBase is the PCI ECAM base address for x86_64.
// Not used directly for config access (we use I/O ports instead),
// but kept for compatibility with code that references it.
var pciEcamBase uintptr = 0xE0000000

// PCI_MMIO_BASE is the PCI MMIO window for BAR reprogramming on x86_64.
// On QEMU q35, 0xC0000000 is in the MMIO region below 4GB.
const PCI_MMIO_BASE = uintptr(0xC0000000)

// Assembly-implemented I/O port PCI config access
func pciIOConfigRead32(addr uint32) uint32
func pciIOConfigWrite32(addr uint32, value uint32)

// pciConfigAddr builds the CF8 address word for PCI config access.
//
//go:nosplit
func pciConfigAddr(bus, slot, funcNum, offset uint8) uint32 {
	return 0x80000000 | // Enable bit
		uint32(bus)<<16 |
		uint32(slot)<<11 |
		uint32(funcNum)<<8 |
		uint32(offset&0xFC)
}

// ConfigRead32 reads a 32-bit value from PCI configuration space via I/O ports.
// Uses the traditional CF8/CFC mechanism which works without ECAM MMIO mapping.
//
//go:nosplit
func ConfigRead32(bus, slot, funcNum, offset uint8) uint32 {
	addr := pciConfigAddr(bus, slot, funcNum, offset)
	pciFirstAccess = false
	return pciIOConfigRead32(addr)
}

// ConfigWrite32 writes a 32-bit value to PCI configuration space via I/O ports.
//
//go:nosplit
func ConfigWrite32(bus, slot, funcNum, offset uint8, value uint32) {
	addr := pciConfigAddr(bus, slot, funcNum, offset)
	pciIOConfigWrite32(addr, value)
}
