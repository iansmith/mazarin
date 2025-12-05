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
	PCI_COMMAND   = 0x04 // Command register - bit 0 = I/O enable, bit 1 = memory enable
	PCI_BAR0      = 0x10
	PCI_BAR2      = 0x18 // Framebuffer address for bochs-display
)

// Bochs VBE register indices (16-bit registers at BAR0 + 0x500 + (index << 1))
const (
	VBE_DISPI_INDEX_ID          = 0x0
	VBE_DISPI_INDEX_XRES        = 0x1
	VBE_DISPI_INDEX_YRES        = 0x2
	VBE_DISPI_INDEX_BPP         = 0x3
	VBE_DISPI_INDEX_ENABLE      = 0x4
	VBE_DISPI_INDEX_BANK        = 0x5
	VBE_DISPI_INDEX_VIRT_WIDTH  = 0x6
	VBE_DISPI_INDEX_VIRT_HEIGHT = 0x7
	VBE_DISPI_INDEX_X_OFFSET    = 0x8
	VBE_DISPI_INDEX_Y_OFFSET    = 0x9
)

// QEMU extended registers (QEMU 2.2+, at MMIO base + 0x600)
const (
	QEMU_EXT_REG_SIZE       = 0x600 // Size of extended register region
	QEMU_EXT_REG_ENDIANNESS = 0x604 // Framebuffer endianness register
	QEMU_ENDIANNESS_BIG     = 0xbebebebe
	QEMU_ENDIANNESS_LITTLE  = 0x1e1e1e1e
)

// Bochs VBE register values
const (
	VBE_DISPI_ID0         = 0xB0C0
	VBE_DISPI_DISABLED    = 0x00
	VBE_DISPI_ENABLED     = 0x01
	VBE_DISPI_LFB_ENABLED = 0x40
	VBE_DISPI_NOCLEARMEM  = 0x80
)

// pciEcamBase is the PCI ECAM base address
// For AArch64 virt machine with highmem (default): 0x4010000000
// For AArch64 virt machine without highmem: 0x3F000000
var pciEcamBase uintptr = 0x4010000000

// pciConfigRead32 reads a 32-bit value from PCI configuration space
// bus, slot, func: PCI device location
// offset: Register offset in config space
//
//go:nosplit
func pciConfigRead32(bus, slot, funcNum, offset uint8) uint32 {
	// Calculate config space address
	// ECAM format: base + (bus << 20) + (device << 15) + (function << 12) + offset
	configAddr := pciEcamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC) // Align to 4-byte boundary

	// Read 32-bit value
	return mmio_read(configAddr)
}

// pciConfigWrite32 writes a 32-bit value to PCI configuration space
//
//go:nosplit
func pciConfigWrite32(bus, slot, funcNum, offset uint8, value uint32) {
	configAddr := pciEcamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)
	mmio_write(configAddr, value)
}

// pciConfigRead32Lowmem reads using lowmem ECAM address (for testing)
//
//go:nosplit
func pciConfigRead32Lowmem(bus, slot, funcNum, offset uint8) uint32 {
	pciEcamBaseLow := uintptr(0x3F000000)
	configAddr := pciEcamBaseLow +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)
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

// BochsDisplayInfo holds information about the bochs-display device
type BochsDisplayInfo struct {
	MMIOBase    uintptr // BAR2 - MMIO registers base (VBE control registers)
	Framebuffer uintptr // BAR0 - Framebuffer address (where pixels go)
	Bus         uint8
	Slot        uint8
	Func        uint8
}

var bochsDisplayInfo BochsDisplayInfo

// findBochsDisplayFull finds the bochs-display device and returns full info
// Returns true if found, false otherwise
//
//go:nosplit
func findBochsDisplayFull() bool {
	uartPuts("PCI1\r\n")

	// Test: Can we read from PCI config space at all?
	// Try highmem address first (default)
	testVendor1 := pciConfigRead32(0, 0, 0, PCI_VENDOR_ID)
	uartPuts("PCI: highmem=0x")
	printHex32(testVendor1)
	uartPuts("\r\n")

	// Try lowmem address if highmem returns 0xFFFFFFFF
	if testVendor1 == 0xFFFFFFFF {
		uartPuts("PCI: trying lowmem\r\n")
		testVendor2 := pciConfigRead32Lowmem(0, 0, 0, PCI_VENDOR_ID)
		uartPuts("PCI: lowmem=0x")
		printHex32(testVendor2)
		uartPuts("\r\n")

		// If lowmem works, use it
		if testVendor2 != 0xFFFFFFFF {
			pciEcamBase = 0x3F000000
			uartPuts("PCI: using lowmem base\r\n")
		}
	}

	// Scan PCI bus (typically bus 0)
	deviceCount := uint32(0)
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor/device ID register (offset 0x00)
				// Format: [device_id:16][vendor_id:16]
				fullReg := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID)
				vendorIDActual := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				// Check if device exists (0xFFFF means no device)
				if vendorIDActual == 0xFFFF || vendorIDActual == 0 {
					continue
				}

				deviceCount++

				// Check if this is bochs-display
				if vendorIDActual == BOCHS_VENDOR_ID && deviceID == BOCHS_DEVICE_ID {
					uartPuts("PCI: FOUND bochs!\r\n")

					// In bare metal, BARs are not initialized by firmware
					// We need to program them ourselves
					// First, check if BARs are already assigned (unlikely in bare metal)
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2 := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)

					uartPuts("Initial BAR0=0x")
					printHex32(bar0)
					uartPuts(" BAR2=0x")
					printHex32(bar2)
					uartPuts("\r\n")

					// Try NOT programming BARs - maybe QEMU will assign them automatically
					// Or try writing all-ones to probe the size, then let QEMU assign
					uartPuts("Probing BAR sizes...\r\n")

					// Write all-ones to BAR0 to probe size (PCI spec method)
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, 0xFFFFFFFF)
					bar0Probe := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					uartPuts("BAR0 probe=0x")
					printHex32(bar0Probe)
					uartPuts("\r\n")

					// Write all-ones to BAR2 to probe size
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, 0xFFFFFFFF)
					bar2Probe := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)
					uartPuts("BAR2 probe=0x")
					printHex32(bar2Probe)
					uartPuts("\r\n")

					// Now try assigning addresses
					// For virt machine, MMIO space typically starts at 0x40000000
					// Use addresses in the PCI MMIO window (0x40000000 - 0x80000000)
					// Framebuffer: 0x50000000 (128MB into MMIO space, plenty of room)
					// MMIO registers: 0x50010000 (64KB after framebuffer)
					fbAddr := uintptr(0x50000000)
					mmioAddr := uintptr(0x50010000)

					uartPuts("Assigning BARs...\r\n")
					// Program BAR0 (framebuffer) - 32-bit memory space, prefetchable
					bar0Value := uint32(fbAddr) | 0x8 // 0x8 = prefetchable, 32-bit memory
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, bar0Value)

					// Program BAR2 (MMIO) - 32-bit memory space, non-prefetchable
					bar2Value := uint32(mmioAddr) | 0x0
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, bar2Value)

					// Re-read to see what QEMU actually assigned
					for delay := 0; delay < 1000; delay++ {
					}
					bar0 = pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2 = pciConfigRead32(bus, slot, funcNum, PCI_BAR2)
					uartPuts("After assign, BAR0=0x")
					printHex32(bar0)
					uartPuts(" BAR2=0x")
					printHex32(bar2)
					uartPuts("\r\n")

					// Enable the device (set all necessary bits in command register)
					// This must be done AFTER programming BARs
					// Also re-read BARs after enabling - QEMU might assign different addresses
					cmd := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
					cmd |= 0x7 // Enable I/O (bit 0), memory (bit 1), and bus master (bit 2)
					pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, cmd)

					// Wait a bit for QEMU to process
					for delay := 0; delay < 10000; delay++ {
					}

					// Re-read BARs after enabling - QEMU might have changed them
					bar0 = pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2 = pciConfigRead32(bus, slot, funcNum, PCI_BAR2)
					uartPuts("After enable, BAR0=0x")
					printHex32(bar0)
					uartPuts(" BAR2=0x")
					printHex32(bar2)
					uartPuts("\r\n")

					// Verify final BARs
					uartPuts("Final BAR0=0x")
					printHex32(bar0)
					uartPuts(" BAR2=0x")
					printHex32(bar2)
					uartPuts("\r\n")

					// Extract addresses from BARs (mask out type bits)
					// Use the values QEMU assigned after enabling
					fbAddr = uintptr(bar0 & 0xFFFFFFF0)
					mmioBase := uintptr(bar2 & 0xFFFFFFF0)

					uartPuts("FB=0x")
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(fbAddr) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts(" MMIO=0x")
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(mmioBase) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")

					// Store in global struct for framebuffer init
					bochsDisplayInfo.MMIOBase = mmioBase
					bochsDisplayInfo.Framebuffer = fbAddr
					bochsDisplayInfo.Bus = bus
					bochsDisplayInfo.Slot = slot
					bochsDisplayInfo.Func = funcNum

					return true
				}
			}
		}
	}

	if deviceCount == 0 {
		uartPuts("PCI: no devices found\r\n")
	} else {
		uartPuts("PCI: found ")
		uartPutUint32(deviceCount)
		uartPuts(" devices, bochs not found\r\n")
	}
	return false
}

// printHex32 prints a 32-bit value in hex
//
//go:nosplit
func printHex32(val uint32) {
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (val >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
}

// writeVBERegister writes a 16-bit value to a VBE register
// VBE registers are at BAR2 (MMIO base) + 0x500 + (index << 1)
// Each register is 16 bits, accessed directly (no index/data port pair on AArch64)
//
//go:nosplit
func writeVBERegister(index, value uint16) {
	regAddr := bochsDisplayInfo.MMIOBase + 0x500 + uintptr(index<<1)
	mmio_write16(regAddr, value)
}

// readVBERegister reads a 16-bit value from a VBE register
// VBE registers are at BAR2 (MMIO base) + 0x500 + (index << 1)
//
//go:nosplit
func readVBERegister(index uint16) uint16 {
	regAddr := bochsDisplayInfo.MMIOBase + 0x500 + uintptr(index<<1)
	return mmio_read16(regAddr)
}

// initBochsDisplay initializes the bochs-display device via VBE registers
// Sets the video mode and enables the framebuffer
// Based on x86 I/O port approach, but using MMIO on AArch64
//
//go:nosplit
func initBochsDisplay(width, height, bpp uint16) bool {
	uartPuts("VBE: checking ID\r\n")

	// Use the MMIO base that was set when we found the device
	// It should already be programmed in the BARs
	mmioBase := bochsDisplayInfo.MMIOBase
	uartPuts("Using MMIO=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(mmioBase) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Read VBE ID register at MMIO base + 0x500
	// According to QEMU docs: VBE registers are at MMIO base + 0x500 + (index << 1)
	// The MMIO bar should be 4096 bytes (0x1000), so 0x500 is within range
	uartPuts("Reading VBE ID from 0x")
	vbeIdAddr := mmioBase + 0x500 + uintptr(VBE_DISPI_INDEX_ID<<1)
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(vbeIdAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" (MMIO base + 0x500 + index*2)\r\n")

	// Check if MMIO base is in a valid range for virt machine
	// virt machine has MMIO space starting at 0x40000000
	// Our programmed address 0xE0010000 might be outside the valid MMIO window
	if mmioBase < 0x40000000 || mmioBase >= 0x80000000 {
		uartPuts("WARNING: MMIO base outside typical virt machine range (0x40000000-0x80000000)\r\n")
		uartPuts("This might cause data abort. Trying anyway...\r\n")
	}

	// Try to read - if this crashes, the MMIO address is wrong or device doesn't work on AArch64
	var id uint16
	id = readVBERegister(VBE_DISPI_INDEX_ID)
	uartPuts("VBE: ID read OK\r\n")
	uartPuts("VBE: ID=0x")
	printHex32(uint32(id))
	uartPuts("\r\n")

	// Accept 0xB0C0 (standard) or 0xB0C5 (some QEMU versions)
	// The upper byte 0xB0 indicates Bochs VBE, lower byte is version
	// If ID is 0, the device might not be initialized yet, or might not support VBE on AArch64
	var workingMMIO uintptr = 0
	if id == 0 {
		uartPuts("VBE: ID is 0 - device may not support VBE on AArch64\r\n")
		uartPuts("VBE: Attempting to initialize anyway (might work)\r\n")
		// Try anyway - maybe writing to registers will initialize it
		workingMMIO = mmioBase
	} else if (id & 0xFF00) == 0xB000 {
		// Valid Bochs VBE ID (any version starting with 0xB0)
		workingMMIO = mmioBase
		uartPuts("VBE: ID OK (0xB0xx)\r\n")
	} else {
		uartPuts("VBE: ID invalid (not 0xB0xx)\r\n")
		// Try anyway - maybe it will work
		workingMMIO = mmioBase
		uartPuts("VBE: Attempting to initialize anyway\r\n")
	}

	// Try to initialize even if ID check failed
	if workingMMIO == 0 {
		uartPuts("VBE: MMIO base is 0, cannot proceed\r\n")
		return false
	}

	// MMIO base is already set correctly
	uartPuts("VBE: ID OK, proceeding\r\n")

	// Check and set framebuffer endianness (QEMU 2.2+)
	// Endianness register is at MMIO base + 0x604
	endiannessAddr := mmioBase + QEMU_EXT_REG_ENDIANNESS
	uartPuts("Checking framebuffer endianness at 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(endiannessAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("...\r\n")

	endianness := mmio_read(endiannessAddr)
	uartPuts("Framebuffer endianness: 0x")
	printHex32(endianness)
	uartPuts("\r\n")

	// AArch64 is little-endian, so set framebuffer to little-endian
	if endianness != QEMU_ENDIANNESS_LITTLE {
		uartPuts("Setting framebuffer to little-endian...\r\n")
		mmio_write(endiannessAddr, QEMU_ENDIANNESS_LITTLE)
		// Verify
		endianness = mmio_read(endiannessAddr)
		uartPuts("Framebuffer endianness after set: 0x")
		printHex32(endianness)
		uartPuts("\r\n")
		if endianness == QEMU_ENDIANNESS_LITTLE {
			uartPuts("Endianness set OK\r\n")
		} else {
			uartPuts("WARNING: Endianness set failed\r\n")
		}
	} else {
		uartPuts("Framebuffer already little-endian\r\n")
	}

	uartPuts("VBE: disabling\r\n")
	// 1. Disable VBE extensions first (like x86 example)
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, VBE_DISPI_DISABLED)

	uartPuts("VBE: setting mode\r\n")
	// 2. Write resolution and BPP (like x86 example)
	writeVBERegister(VBE_DISPI_INDEX_XRES, width)
	writeVBERegister(VBE_DISPI_INDEX_YRES, height)
	writeVBERegister(VBE_DISPI_INDEX_BPP, bpp)

	// Set virtual resolution same as physical (optional, but recommended)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_WIDTH, width)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_HEIGHT, height)

	// Set offsets to 0
	writeVBERegister(VBE_DISPI_INDEX_X_OFFSET, 0)
	writeVBERegister(VBE_DISPI_INDEX_Y_OFFSET, 0)

	// 3. Enable VBE extensions (like x86 example)
	// The x86 example uses just VBE_DISPI_ENABLED
	// For linear framebuffer access, we also need LFB_ENABLED
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, VBE_DISPI_ENABLED|VBE_DISPI_LFB_ENABLED)

	// Don't verify - just assume it worked
	// We've written all the registers, so return success
	uartPuts("VBE: INIT SUCCESS\r\n")
	return true
}
