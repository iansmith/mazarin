//go:build qemuvirt && aarch64

package main

import "mazboot/asm"

// SDHCI initialization for QEMU virt machine
//
// QEMU virt machine uses SDHCI as a PCI device.
// This implementation finds the device via PCI enumeration
// and reads the MMIO base address from PCI BAR0.

// SDHCI PCI device IDs (for QEMU virt machine)
const (
	SDHCI_VENDOR_ID = 0x1B36 // QEMU vendor ID
	SDHCI_DEVICE_ID = 0x0001 // SDHCI device ID (may vary, check PCI enumeration)
)

// sdhciInit initializes the SDHCI controller for QEMU virt machine
// Finds the device via PCI enumeration and sets up MMIO access
// Returns true if initialization successful
//
//go:nosplit
func sdhciInit() bool {
	// Scan PCI bus (typically bus 0)
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor/device ID register (offset 0x00)
				fullReg := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID)
				vendorID := fullReg & 0xFFFF
				_ = (fullReg >> 16) & 0xFFFF // deviceID - unused

				// Check if device exists (0xFFFF means no device)
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Check if this is SDHCI device
				if vendorID == SDHCI_VENDOR_ID {
					// Enable the device (memory and I/O space)
					cmd := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
					cmd |= 0x7 // Enable I/O (bit 0), memory (bit 1), and bus master (bit 2)
					pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, cmd)

					// Wait for QEMU to process the command register change
					for delay := 0; delay < 1000; delay++ {
					}

					// Read BAR0 (MMIO base address)
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)

					// BAR0 lower bits indicate type - mask them out
					// Bit 0 = 0 means memory space, bits 2-1 indicate size
					// For 32-bit memory space, mask out lower 4 bits
					mmioBase := uintptr(bar0 & 0xFFFFFFF0)

					// Verify this is actually SDHCI by reading Host Version register
					// This should return a non-zero value for valid SDHCI
					version := asm.MmioRead16(mmioBase + SDHCI_HOST_VERSION)

					if version == 0 || version == 0xFFFF {
						continue
					}

					// Set global MMIO base
					sdhciMMIOBase = mmioBase

					// Enable interrupts (basic set)
					intEnable := uint16(SDHCI_INT_CMD_COMPLETE | SDHCI_INT_XFER_COMPLETE | SDHCI_INT_ERROR)
					sdhciWrite16(SDHCI_INT_ENABLE, intEnable)
					sdhciWrite16(SDHCI_SIGNAL_ENABLE, intEnable)

					return true
				}
			}
		}
	}

	return false
}



