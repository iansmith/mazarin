//go:build raspi5

package main

// SDHCI initialization for Raspberry Pi 5
//
// Raspberry Pi 5 uses the Arasan SDHCI controller at fixed MMIO address 0xFE300000.
// No PCI enumeration needed - the address is fixed in hardware.
// Note: Pi 5 may use the same address as Pi 4, but kept separate for future differences.

// Raspberry Pi 5 SD card controller (Arasan SDHCI) base address
// BCM2712 peripheral address space: 0xFE000000 - 0xFEFFFFFF
// SD card controller is at offset 0x300000 from peripheral base
// (Same address as Pi 4, but kept separate for potential future differences)
const (
	RPI5_SDHCI_BASE = PERIPHERAL_BASE + 0x300000 // 0xFE300000 for Pi 5
)

// sdhciInit initializes the SDHCI controller for Raspberry Pi 5
// Uses fixed MMIO address - no enumeration needed
// Returns true if initialization successful
//
//go:nosplit
func sdhciInit() bool {
	// Set global MMIO base
	sdhciMMIOBase = RPI5_SDHCI_BASE

	// Read capabilities and present state (needed for card detection)
	_ = sdhciRead32(SDHCI_CAPABILITIES)
	_ = sdhciRead32(SDHCI_PRESENT_STATE)
	_ = sdhciRead16(SDHCI_HOST_VERSION)

	// Enable interrupts (basic set)
	intEnable := uint16(SDHCI_INT_CMD_COMPLETE | SDHCI_INT_XFER_COMPLETE | SDHCI_INT_ERROR)
	sdhciWrite16(SDHCI_INT_ENABLE, intEnable)
	sdhciWrite16(SDHCI_SIGNAL_ENABLE, intEnable)

	return true
}



