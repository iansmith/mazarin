//go:build raspi4

package main

// SDHCI initialization for Raspberry Pi 4
//
// Raspberry Pi 4 uses the Arasan SDHCI controller at fixed MMIO address 0xFE300000.
// No PCI enumeration needed - the address is fixed in hardware.

// Raspberry Pi 4 SD card controller (Arasan SDHCI) base address
// BCM2711 peripheral address space: 0xFE000000 - 0xFEFFFFFF
// SD card controller is at offset 0x300000 from peripheral base
const (
	RPI4_SDHCI_BASE = PERIPHERAL_BASE + 0x300000 // 0xFE300000 for Pi 4
)

// sdhciInit initializes the SDHCI controller for Raspberry Pi 4
// Uses fixed MMIO address - no enumeration needed
// Returns true if initialization successful
//
//go:nosplit
func sdhciInit() bool {
	uartPuts("SDHCI: Initializing for Raspberry Pi 4 (Arasan SDHCI at 0xFE300000)\r\n")

	// Set global MMIO base
	sdhciMMIOBase = RPI4_SDHCI_BASE

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

	// Read host version
	version := sdhciRead16(SDHCI_HOST_VERSION)
	uartPuts("SDHCI: Host Version: 0x")
	uartPutHex32(uint32(version))
	uartPuts("\r\n")

	// Enable interrupts (basic set)
	intEnable := uint16(SDHCI_INT_CMD_COMPLETE | SDHCI_INT_XFER_COMPLETE | SDHCI_INT_ERROR)
	sdhciWrite16(SDHCI_INT_ENABLE, intEnable)
	sdhciWrite16(SDHCI_SIGNAL_ENABLE, intEnable)

	uartPuts("SDHCI: Initialization complete\r\n")
	return true
}


