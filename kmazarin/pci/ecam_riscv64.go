package pci

import "mazzy/kmazarin/asm"

// pciEcamBase is the PCI ECAM base address for RISC-V.
// QEMU virt machine: 0x30000000
var pciEcamBase uintptr = 0x30000000

// PCI_MMIO_BASE is the PCI MMIO window for BAR reprogramming on RISC-V.
// On QEMU virt, 0x40000000 is the PCI MMIO aperture.
const PCI_MMIO_BASE = uintptr(0x40000000)

// ConfigRead32 reads a 32-bit value from PCI configuration space via ECAM.
//
//go:nosplit
func ConfigRead32(bus, slot, funcNum, offset uint8) uint32 {
	ecamBase := pciEcamBase
	if ecamBase == 0 {
		return 0xFFFFFFFF
	}

	configAddr := ecamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)

	pciFirstAccess = false
	return asm.MmioRead(configAddr)
}

// ConfigWrite32 writes a 32-bit value to PCI configuration space via ECAM.
//
//go:nosplit
func ConfigWrite32(bus, slot, funcNum, offset uint8, value uint32) {
	configAddr := pciEcamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)
	asm.MmioWrite(configAddr, value)
}
