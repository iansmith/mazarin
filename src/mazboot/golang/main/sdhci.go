package main

import (
	"mazboot/asm"
	"unsafe"
)

// SDHCI (SD Host Controller Interface) - Unified API
//
// This file provides the common API for SDHCI access across all platforms.
// Platform-specific initialization is handled in separate files:
//   - sdhci_init_qemu.go: QEMU virt machine (PCI-based)
//   - sdhci_init_rpi4.go: Raspberry Pi 4 (direct MMIO)
//   - sdhci_init_rpi5.go: Raspberry Pi 5 (direct MMIO)
//
// SDHCI Specification: SD Host Controller Simplified Specification v3.00
// Standard register set: 256 bytes (0x00 - 0xFF)
// Vendor-specific registers: beyond 0xFF

// SDHCI standard register offsets (from base MMIO address)
const (
	// DMA and Transfer registers
	SDHCI_DMA_ADDRESS   = 0x00 // SDMA System Address (64-bit, two 32-bit registers)
	SDHCI_BLOCK_SIZE    = 0x04 // Block Size (16-bit) and Block Count (16-bit)
	SDHCI_ARGUMENT      = 0x08 // Argument (32-bit)
	SDHCI_TRANSFER_MODE = 0x0C // Transfer Mode (16-bit) and Command (16-bit)
	SDHCI_COMMAND       = 0x0E // Command (16-bit)

	// Response registers (read-only)
	SDHCI_RESPONSE_0 = 0x10 // Response[31:0]
	SDHCI_RESPONSE_1 = 0x14 // Response[63:32]
	SDHCI_RESPONSE_2 = 0x18 // Response[95:64]
	SDHCI_RESPONSE_3 = 0x1C // Response[127:96]

	// Buffer Data Port
	SDHCI_BUFFER = 0x20 // Buffer Data Port (32-bit)

	// Present State register
	SDHCI_PRESENT_STATE = 0x24 // Present State (32-bit)

	// Host Control registers
	SDHCI_HOST_CTRL      = 0x28 // Host Control 1 (8-bit)
	SDHCI_POWER_CTRL     = 0x29 // Power Control (8-bit)
	SDHCI_BLOCK_GAP_CTRL = 0x2A // Block Gap Control (8-bit)
	SDHCI_WAKEUP_CTRL    = 0x2B // Wakeup Control (8-bit)
	SDHCI_CLOCK_CTRL     = 0x2C // Clock Control (16-bit)
	SDHCI_TIMEOUT_CTRL   = 0x2E // Timeout Control (8-bit)
	SDHCI_SOFTWARE_RESET = 0x2F // Software Reset (8-bit)

	// Interrupt registers
	SDHCI_INT_STATUS    = 0x30 // Interrupt Status (16-bit)
	SDHCI_INT_ENABLE    = 0x34 // Interrupt Enable (16-bit)
	SDHCI_SIGNAL_ENABLE = 0x38 // Interrupt Signal Enable (16-bit)

	// Capabilities registers
	SDHCI_CAPABILITIES   = 0x40 // Capabilities (32-bit)
	SDHCI_CAPABILITIES_1 = 0x44 // Capabilities 1 (32-bit)

	// Maximum Current registers
	SDHCI_MAX_CURRENT = 0x48 // Maximum Current Capabilities (32-bit)

	// Force Event registers (write-only)
	SDHCI_FORCE_EVENT     = 0x50 // Force Event (16-bit)
	SDHCI_FORCE_EVENT_ERR = 0x52 // Force Event Error Status (16-bit)

	// ADMA registers (if ADMA supported)
	SDHCI_ADMA_SYS_ADDR   = 0x58 // ADMA System Address (64-bit, two 32-bit registers)
	SDHCI_ADMA_SYS_ADDR_1 = 0x5C // ADMA System Address [63:32]

	// Slot Interrupt Status and Version
	SDHCI_SLOT_INT_STATUS = 0xFC // Slot Interrupt Status (16-bit)
	SDHCI_HOST_VERSION    = 0xFE // Host Controller Version (16-bit)
)

// SDHCI Present State register bits
const (
	SDHCI_CMD_INHIBIT     = 1 << 0  // Command Inhibit (CMD)
	SDHCI_CMD_INHIBIT_DAT = 1 << 1  // Command Inhibit (DAT)
	SDHCI_DAT_LINE_ACTIVE = 1 << 2  // DAT Line Active
	SDHCI_RE_TUNING_REQ   = 1 << 3  // Re-Tuning Request
	SDHCI_WRITE_PROTECT   = 1 << 19 // Write Protect
	SDHCI_CARD_DETECT     = 1 << 16 // Card Detect
	SDHCI_CARD_STABLE     = 1 << 20 // Card State Stable
	SDHCI_CARD_PRESENT    = 1 << 18 // Card Present
)

// SDHCI Host Control register bits
const (
	SDHCI_LED_CONTROL     = 1 << 0 // LED Control
	SDHCI_DATA_WIDTH_4BIT = 1 << 1 // 4-bit data width
	SDHCI_HIGH_SPEED      = 1 << 2 // High Speed Enable
	SDHCI_DMA_ENABLE      = 1 << 3 // DMA Enable
	SDHCI_ADMA_ENABLE     = 1 << 4 // ADMA Enable (if supported)
)

// SDHCI Interrupt Status bits
const (
	SDHCI_INT_CMD_COMPLETE  = 1 << 0  // Command Complete
	SDHCI_INT_XFER_COMPLETE = 1 << 1  // Transfer Complete
	SDHCI_INT_BLOCK_GAP     = 1 << 2  // Block Gap Event
	SDHCI_INT_DMA_END       = 1 << 3  // DMA End
	SDHCI_INT_BUFFER_WRITE  = 1 << 4  // Buffer Write Ready
	SDHCI_INT_BUFFER_READ   = 1 << 5  // Buffer Read Ready
	SDHCI_INT_CARD_INSERT   = 1 << 6  // Card Insert
	SDHCI_INT_CARD_REMOVE   = 1 << 7  // Card Remove
	SDHCI_INT_CARD_INT      = 1 << 8  // Card Interrupt
	SDHCI_INT_ERROR         = 1 << 15 // Error
)

// SDHCI Command register bits
const (
	SDHCI_CMD_RESPONSE_NONE    = 0 << 0 // No response
	SDHCI_CMD_RESPONSE_136     = 1 << 0 // 136-bit response
	SDHCI_CMD_RESPONSE_48      = 2 << 0 // 48-bit response
	SDHCI_CMD_RESPONSE_48_BUSY = 3 << 0 // 48-bit response with busy

	SDHCI_CMD_DATA        = 1 << 5  // Data present
	SDHCI_CMD_READ        = 1 << 4  // Read
	SDHCI_CMD_MULTI_BLOCK = 1 << 5  // Multiple block
	SDHCI_CMD_STOP        = 1 << 12 // Stop command
	SDHCI_CMD_INDEX_MASK  = 0x3F    // Command index mask (bits 5-0)
)

// sdhciMMIOBase is the MMIO base address for SDHCI registers
// Set by platform-specific initialization
var sdhciMMIOBase uintptr = 0

// sdhciRead16 reads a 16-bit register from SDHCI
//
//go:nosplit
func sdhciRead16(offset uintptr) uint16 {
	if sdhciMMIOBase == 0 {
		return 0
	}
	return asm.MmioRead16(sdhciMMIOBase + offset)
}

// sdhciWrite16 writes a 16-bit register to SDHCI
//
//go:nosplit
func sdhciWrite16(offset uintptr, value uint16) {
	if sdhciMMIOBase == 0 {
		return
	}
	asm.MmioWrite16(sdhciMMIOBase+offset, value)
	asm.Dsb() // Memory barrier
}

// sdhciRead32 reads a 32-bit register from SDHCI
//
//go:nosplit
func sdhciRead32(offset uintptr) uint32 {
	if sdhciMMIOBase == 0 {
		return 0
	}
	return asm.MmioRead(sdhciMMIOBase + offset)
}

// sdhciWrite32 writes a 32-bit register to SDHCI
//
//go:nosplit
func sdhciWrite32(offset uintptr, value uint32) {
	if sdhciMMIOBase == 0 {
		return
	}
	asm.MmioWrite(sdhciMMIOBase+offset, value)
	asm.Dsb() // Memory barrier
}

// sdhciWaitReady waits for the controller to be ready for commands
// Returns true if ready, false on timeout
//
//go:nosplit
func sdhciWaitReady() bool {
	timeout := 1000000 // Timeout counter
	for timeout > 0 {
		presentState := sdhciRead32(SDHCI_PRESENT_STATE)
		// Check if command and data lines are not inhibited
		if (presentState & (SDHCI_CMD_INHIBIT | SDHCI_CMD_INHIBIT_DAT)) == 0 {
			return true
		}
		timeout--
	}
	return false
}

// sdhciSendCommand sends a command to the SD card
// This is a basic example - full SD card initialization requires
// following the SD card specification initialization sequence
//
//go:nosplit
func sdhciSendCommand(cmdIndex uint8, arg uint32, flags uint16) bool {
	if !sdhciWaitReady() {
		return false
	}

	// Clear interrupt status
	sdhciWrite16(SDHCI_INT_STATUS, 0xFFFF)

	// Set argument
	sdhciWrite32(SDHCI_ARGUMENT, arg)

	// Set command (index + flags)
	cmd := uint16(cmdIndex) | flags
	sdhciWrite16(SDHCI_COMMAND, cmd)

	// Wait for command complete interrupt
	timeout := 1000000
	for timeout > 0 {
		intStatus := sdhciRead16(SDHCI_INT_STATUS)
		if (intStatus & SDHCI_INT_CMD_COMPLETE) != 0 {
			sdhciWrite16(SDHCI_INT_STATUS, SDHCI_INT_CMD_COMPLETE)
			return true
		}
		if (intStatus & SDHCI_INT_ERROR) != 0 {
			sdhciWrite16(SDHCI_INT_STATUS, SDHCI_INT_ERROR)
			return false
		}
		timeout--
	}

	return false
}

// sdhciGetResponse reads the response from the last command
// Returns the 32-bit response (for 48-bit responses, call multiple times)
//
//go:nosplit
func sdhciGetResponse(responseNum uint8) uint32 {
	if responseNum > 3 {
		return 0
	}
	offset := SDHCI_RESPONSE_0 + uintptr(responseNum*4)
	return sdhciRead32(offset)
}

// sdhciReadBlock reads a single block from the SD card
// blockNum: Block number to read
// buffer: Buffer to store the data (must be at least 512 bytes)
// Returns true on success, false on error
//
//go:nosplit
func sdhciReadBlock(blockNum uint32, buffer unsafe.Pointer) bool {
	// TODO: Implement block read using CMD17 (READ_SINGLE_BLOCK)
	// This requires proper SD card initialization first
	_ = blockNum
	_ = buffer
	return false
}

// sdhciWriteBlock writes a single block to the SD card
// blockNum: Block number to write
// buffer: Buffer containing the data (must be at least 512 bytes)
// Returns true on success, false on error
//
//go:nosplit
func sdhciWriteBlock(blockNum uint32, buffer unsafe.Pointer) bool {
	// TODO: Implement block write using CMD24 (WRITE_BLOCK)
	// This requires proper SD card initialization first
	_ = blockNum
	_ = buffer
	return false
}



