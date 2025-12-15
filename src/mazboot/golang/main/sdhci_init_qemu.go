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
	uartPuts("SDHCI: Initializing for QEMU virt machine (PCI-based)\r\n")
	uartPuts("SDHCI: Scanning PCI bus for SDHCI device...\r\n")

	// Scan PCI bus (typically bus 0)
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor/device ID register (offset 0x00)
				fullReg := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID)
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				// Check if device exists (0xFFFF means no device)
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Check if this is SDHCI device
				if vendorID == SDHCI_VENDOR_ID {
					uartPuts("SDHCI: Found potential SDHCI device\r\n")
					uartPuts("  Vendor ID: 0x")
					uartPutHex32(vendorID)
					uartPuts(", Device ID: 0x")
					uartPutHex32(deviceID)
					uartPuts("\r\n")
					uartPuts("  Bus: 0x")
					uartPutHex32(uint32(bus))
					uartPuts(", Slot: 0x")
					uartPutHex32(uint32(slot))
					uartPuts(", Func: 0x")
					uartPutHex32(uint32(funcNum))
					uartPuts("\r\n")

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

					uartPuts("SDHCI: BAR0 (MMIO base): 0x")
					uartPutHex32(bar0)
					uartPuts("\r\n")
					uartPuts("SDHCI: MMIO base address: 0x")
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(mmioBase) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")

					// Verify this is actually SDHCI by reading Host Version register
					// This should return a non-zero value for valid SDHCI
					version := asm.MmioRead16(mmioBase + SDHCI_HOST_VERSION)
					uartPuts("SDHCI: Host Version: 0x")
					uartPutHex32(uint32(version))
					uartPuts("\r\n")

					if version == 0 || version == 0xFFFF {
						uartPuts("SDHCI: WARNING - Invalid version, may not be SDHCI\r\n")
						continue
					}

					// Set global MMIO base
					sdhciMMIOBase = mmioBase

					// Read and display capabilities
					capabilities := sdhciRead32(SDHCI_CAPABILITIES)
					uartPuts("SDHCI: Capabilities: 0x")
					uartPutHex32(capabilities)
					uartPuts("\r\n")

					// Read present state
					presentState := sdhciRead32(SDHCI_PRESENT_STATE)
					uartPuts("SDHCI: Present State: 0x")
					uartPutHex32(presentState)
					uartPuts("\r\n")

					// Check if card is present
					if (presentState & SDHCI_CARD_PRESENT) != 0 {
						uartPuts("SDHCI: Card detected\r\n")
					} else {
						uartPuts("SDHCI: No card detected\r\n")
					}

					// Enable interrupts (basic set)
					intEnable := uint16(SDHCI_INT_CMD_COMPLETE | SDHCI_INT_XFER_COMPLETE | SDHCI_INT_ERROR)
					sdhciWrite16(SDHCI_INT_ENABLE, intEnable)
					sdhciWrite16(SDHCI_SIGNAL_ENABLE, intEnable)

					uartPuts("SDHCI: Initialization complete\r\n")
					return true
				}
			}
		}
	}

	uartPuts("SDHCI: SDHCI device not found\r\n")
	return false
}


