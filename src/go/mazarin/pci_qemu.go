//go:build qemu

package main

// PCI configuration space constants
const (
	PCI_CONFIG_ADDRESS = 0x0CF8 // I/O port for PCI config address
	PCI_CONFIG_DATA    = 0x0CFC // I/O port for PCI config data

	// bochs-display device IDs
	BOCHS_VENDOR_ID = 0x1234
	BOCHS_DEVICE_ID = 0x1111

	// PCI configuration space offsets
	PCI_VENDOR_ID = 0x00
	PCI_DEVICE_ID = 0x02
	PCI_BAR0      = 0x10
)

// pciConfigRead32 reads a 32-bit value from PCI configuration space
// bus, slot, func: PCI device location
// offset: Register offset in config space
//
//go:nosplit
func pciConfigRead32(bus, slot, funcNum, offset uint8) uint32 {
	// For AArch64, we can't use I/O ports directly like x86
	// PCI config space on AArch64 virt machine is memory-mapped
	// The config space base is typically at 0x30000000 for virt machine
	// Format: bus << 20 | slot << 15 | func << 12 | offset
	// But this is complex - for now, let's try a simpler approach

	// Actually, on AArch64 virt machine, PCI config space might be accessed differently
	// Let's try memory-mapped access first
	// PCI ECAM (Enhanced Configuration Access Mechanism) base for virt is typically 0x30000000
	pciEcamBase := uintptr(0x30000000)

	// Calculate config space address: bus << 20 | slot << 15 | func << 12 | offset
	configAddr := pciEcamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC) // Align to 4-byte boundary

	// Read 32-bit value
	return mmio_read(configAddr)
}

// findBochsDisplay finds the bochs-display PCI device and returns its BAR0 address
// Returns 0 if not found
//
//go:nosplit
func findBochsDisplay() uintptr {
	uartPuts("findBochsDisplay: Scanning PCI bus...\r\n")

	// Scan PCI bus (typically bus 0)
	// Try common slots where display devices might be
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor ID
				vendorID := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID) & 0xFFFF

				// Check if device exists (vendor ID != 0xFFFF)
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Read device ID
				deviceID := pciConfigRead32(bus, slot, funcNum, PCI_DEVICE_ID) & 0xFFFF

				// Check if this is bochs-display
				if vendorID == BOCHS_VENDOR_ID && deviceID == BOCHS_DEVICE_ID {
					uartPuts("findBochsDisplay: Found bochs-display device\r\n")
					uartPuts("  Bus: ")
					uartPutUint32(uint32(bus))
					uartPuts(", Slot: ")
					uartPutUint32(uint32(slot))
					uartPuts(", Func: ")
					uartPutUint32(uint32(funcNum))
					uartPuts("\r\n")

					// Read BAR0
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)

					// BAR0 lower bits indicate type - mask them out
					// Bit 0 = 0 means memory space, bits 2-1 indicate size
					// For 32-bit memory space, mask out lower 4 bits
					fbAddr := uintptr(bar0 & 0xFFFFFFF0)

					uartPuts("  BAR0: 0x")
					// Print BAR0 in hex
					for shift := 28; shift >= 0; shift -= 4 {
						digit := (bar0 >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")

					uartPuts("  Framebuffer address: 0x")
					// Print framebuffer address in hex
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(fbAddr) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")

					return fbAddr
				}
			}
		}
	}

	uartPuts("findBochsDisplay: bochs-display device not found\r\n")
	return 0
}
