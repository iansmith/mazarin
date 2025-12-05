//go:build qemu

package main

// bochs-display device for QEMU virt machine
//
// BAR mapping (as assigned by QEMU):
//   BAR0: Framebuffer address (memory-mapped, where pixels are written)
//   BAR2: MMIO registers base (memory-mapped, VBE control registers)
//
// VBE register access (AArch64 MMIO):
//   Index register: MMIO base + 0x500
//   Data register:  MMIO base + 0x502
//   Pattern: Write index, then read/write data (matches x86 I/O port approach)
//
// Framebuffer access:
//   Write pixels directly to BAR0 address (framebuffer)
//   Format: XRGB8888 (32-bit per pixel) or RGB888 (24-bit per pixel)
//   Note: bochs-display uses BGR byte order (Blue, Green, Red), not RGB!

// PCI configuration space constants
const (
	PCI_CONFIG_ADDRESS = 0x0CF8 // I/O port for PCI config address
	PCI_CONFIG_DATA    = 0x0CFC // I/O port for PCI config data

	// bochs-display device IDs
	BOCHS_VENDOR_ID = 0x1234
	BOCHS_DEVICE_ID = 0x1111

	// VirtIO device IDs
	VIRTIO_VENDOR_ID     = 0x1AF4
	VIRTIO_GPU_DEVICE_ID = 0x1050

	// PCI configuration space offsets
	PCI_VENDOR_ID    = 0x00
	PCI_DEVICE_ID    = 0x02
	PCI_COMMAND      = 0x04 // Command register - bit 0 = I/O enable, bit 1 = memory enable
	PCI_BAR0         = 0x10 // Framebuffer address for bochs-display
	PCI_BAR2         = 0x18 // MMIO registers base for bochs-display
	PCI_CAPABILITIES = 0x34 // Capabilities pointer (offset to first capability)
)

// PCI capability types
const (
	PCI_CAP_VENDOR_SPECIFIC = 0x09 // VirtIO Common Config
	PCI_CAP_NOTIFY          = 0x0A // VirtIO Notify Config
	PCI_CAP_ISR             = 0x0B // VirtIO ISR Status
	PCI_CAP_DEVICE          = 0x0C // VirtIO Device Config
)

// Bochs VBE register indices (accessed via index/data register pair at MMIO base + 0x500/0x502)
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
// Addresses are read from PCI BARs as assigned by QEMU
type BochsDisplayInfo struct {
	MMIOBase    uintptr // BAR2 - MMIO registers base (VBE control registers at +0x500)
	Framebuffer uintptr // BAR0 - Framebuffer address (where pixels are written)
	Bus         uint8   // PCI bus number
	Slot        uint8   // PCI slot number
	Func        uint8   // PCI function number
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
					uartPuts("PCI: FOUND bochs-display device!\r\n")
					uartPuts("  Bus: 0x")
					printHex32(uint32(bus))
					uartPuts(", Slot: 0x")
					printHex32(uint32(slot))
					uartPuts(", Func: 0x")
					printHex32(uint32(funcNum))
					uartPuts("\r\n")

					// Enable the device first (memory and I/O space)
					// This must be done before reading BARs, as QEMU may assign them on enable
					cmd := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
					uartPuts("Initial command register: 0x")
					printHex32(cmd)
					uartPuts("\r\n")

					cmd |= 0x7 // Enable I/O (bit 0), memory (bit 1), and bus master (bit 2)
					pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, cmd)

					// Wait for QEMU to process the command register change
					for delay := 0; delay < 1000; delay++ {
					}

					// Probe BARs by writing all-ones (PCI spec method to determine size)
					// Save original values first
					bar0Original := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2Original := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)

					uartPuts("PCI: Original BAR0=0x")
					printHex32(bar0Original)
					uartPuts(" BAR2=0x")
					printHex32(bar2Original)
					uartPuts("\r\n")

					// Write all-ones to probe size
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, 0xFFFFFFFF)
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, 0xFFFFFFFF)

					// Read back size masks
					bar0Size := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2Size := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)

					uartPuts("PCI: BAR size masks - BAR0=0x")
					printHex32(bar0Size)
					uartPuts(" BAR2=0x")
					printHex32(bar2Size)
					uartPuts("\r\n")

					// For bare-metal, we need to assign BAR addresses ourselves
					// QEMU virt machine kernel RAM: 0x40100000 - 0x60000000 (512MB)
					// Use fixed addresses within kernel RAM for bochs-display BAR programming
					// 0x50000000 is within our kernel RAM region (between 0x40100000 and 0x60000000)
					fbAddr := uintptr(0x50000000)  // Framebuffer address (within kernel RAM)
					mmioBase := uintptr(0x50010000) // MMIO registers (right after framebuffer)

					// Program BAR0 (framebuffer) - 32-bit memory space, prefetchable
					bar0Value := uint32(fbAddr) | 0x8 // 0x8 = prefetchable, 32-bit memory
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, bar0Value)

					// Program BAR2 (MMIO) - 32-bit memory space, non-prefetchable
					bar2Value := uint32(mmioBase) | 0x0
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, bar2Value)

					// Wait for writes to complete
					for delay := 0; delay < 1000; delay++ {
					}

					// Read back to verify
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2 := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)

					uartPuts("BAR0 (framebuffer): 0x")
					printHex32(bar0)
					uartPuts("\r\n")
					uartPuts("BAR2 (MMIO registers): 0x")
					printHex32(bar2)
					uartPuts("\r\n")

					// Check if BARs are valid (not 0xFFFFFFFF or 0x00000000)
					if bar0 == 0xFFFFFFFF || bar0 == 0 || bar2 == 0xFFFFFFFF || bar2 == 0 {
						uartPuts("ERROR: BARs not assigned by QEMU!\r\n")
						uartPuts("BAR0=0x")
						printHex32(bar0)
						uartPuts(" BAR2=0x")
						printHex32(bar2)
						uartPuts("\r\n")
						return false
					}

					// Extract addresses from BARs (mask out type bits)
					// Bit 0 = 0 means memory space
					// Bits 2-1 indicate size/type
					// For 32-bit memory space, mask out lower 4 bits
					// Use the addresses we programmed (QEMU might modify them)
					fbAddr = uintptr(bar0 & 0xFFFFFFF0)
					mmioBase = uintptr(bar2 & 0xFFFFFFF0)

					uartPuts("Framebuffer address: 0x")
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(fbAddr) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")
					uartPuts("MMIO base address: 0x")
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

// VBE register base offset from MMIO base
// On AArch64 with MMIO, VBE registers are accessed directly at:
//
//	MMIO base + 0x500 + (index << 1)
//
// This is different from x86 which uses I/O port index/data pair
const (
	VBE_DISPI_REG_BASE_OFFSET = 0x500 // Base offset for VBE registers
)

// writeVBERegister writes a 16-bit value directly to a VBE register
// On AArch64 with MMIO, registers are at: MMIO base + 0x500 + (index << 1)
// Each register is 16 bits wide, accessed directly (no index/data port pair)
//
//go:nosplit
func writeVBERegister(index, value uint16) {
	regAddr := bochsDisplayInfo.MMIOBase + VBE_DISPI_REG_BASE_OFFSET + uintptr(index<<1)
	mmio_write16(regAddr, value)
	// Memory barrier to ensure write completes
	dsb()
}

// readVBERegister reads a 16-bit value directly from a VBE register
// On AArch64 with MMIO, registers are at: MMIO base + 0x500 + (index << 1)
//
//go:nosplit
func readVBERegister(index uint16) uint16 {
	regAddr := bochsDisplayInfo.MMIOBase + VBE_DISPI_REG_BASE_OFFSET + uintptr(index<<1)
	return mmio_read16(regAddr)
}

// initBochsDisplay initializes the bochs-display device via VBE registers
// Sets the video mode and enables the framebuffer
// Matches the C code pattern: disable -> set mode -> enable
//
//go:nosplit
func initBochsDisplay(width, height, bpp uint16) bool {
	mmioBase := bochsDisplayInfo.MMIOBase
	if mmioBase == 0 {
		uartPuts("VBE: ERROR - MMIO base is 0\r\n")
		return false
	}

	uartPuts("VBE: Initializing bochs-display\r\n")
	uartPuts("VBE: MMIO base: 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(mmioBase) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Optional: Read and verify VBE ID (for debugging)
	id := readVBERegister(VBE_DISPI_INDEX_ID)
	uartPuts("VBE: ID register: 0x")
	printHex32(uint32(id))
	uartPuts("\r\n")
	if id != 0 && (id&0xFF00) != 0xB000 {
		uartPuts("VBE: WARNING - ID doesn't match expected Bochs VBE (0xB0xx)\r\n")
		uartPuts("VBE: Continuing anyway...\r\n")
	}

	// Check and set framebuffer endianness (QEMU 2.2+)
	// Endianness register is at MMIO base + 0x604
	endiannessAddr := mmioBase + QEMU_EXT_REG_ENDIANNESS
	endianness := mmio_read(endiannessAddr)
	if endianness != QEMU_ENDIANNESS_LITTLE {
		uartPuts("VBE: Setting framebuffer to little-endian...\r\n")
		mmio_write(endiannessAddr, QEMU_ENDIANNESS_LITTLE)
		dsb() // Ensure write completes
	}

	// 1. Disable VBE extensions before changing parameters
	uartPuts("VBE: Disabling display...\r\n")
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, VBE_DISPI_DISABLED)
	uartPuts("VBE: Disable OK\r\n")

	// 2. Set the desired resolution and color depth
	uartPuts("VBE: Setting XRES=")
	printHex32(uint32(width))
	uartPuts("\r\n")
	writeVBERegister(VBE_DISPI_INDEX_XRES, width)
	uartPuts("VBE: XRES OK\r\n")

	uartPuts("VBE: Setting YRES=")
	printHex32(uint32(height))
	uartPuts("\r\n")
	writeVBERegister(VBE_DISPI_INDEX_YRES, height)
	uartPuts("VBE: YRES OK\r\n")

	uartPuts("VBE: Setting BPP=")
	printHex32(uint32(bpp))
	uartPuts("\r\n")
	writeVBERegister(VBE_DISPI_INDEX_BPP, bpp)
	uartPuts("VBE: BPP OK\r\n")

	// Set virtual resolution same as physical (recommended)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_WIDTH, width)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_HEIGHT, height)

	// Set offsets to 0
	writeVBERegister(VBE_DISPI_INDEX_X_OFFSET, 0)
	writeVBERegister(VBE_DISPI_INDEX_Y_OFFSET, 0)

	// 3. Enable VBE extensions and the Linear Frame Buffer (LFB)
	uartPuts("VBE: Enabling display with LFB...\r\n")
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, VBE_DISPI_ENABLED|VBE_DISPI_LFB_ENABLED)

	// Verify enable register was set correctly
	enableReg := readVBERegister(VBE_DISPI_INDEX_ENABLE)
	if (enableReg & (VBE_DISPI_ENABLED | VBE_DISPI_LFB_ENABLED)) != (VBE_DISPI_ENABLED | VBE_DISPI_LFB_ENABLED) {
		uartPuts("VBE: WARNING - Enable register verification failed\r\n")
		uartPuts("VBE: Expected: 0x")
		printHex32(uint32(VBE_DISPI_ENABLED | VBE_DISPI_LFB_ENABLED))
		uartPuts(", Got: 0x")
		printHex32(uint32(enableReg))
		uartPuts("\r\n")
		// Continue anyway - might still work
	}

	uartPuts("VBE: Initialization complete\r\n")
	return true
}

// PCI Capability Reading Functions

// pciConfigRead8 reads an 8-bit value from PCI configuration space
//
//go:nosplit
func pciConfigRead8(bus, slot, funcNum, offset uint8) uint8 {
	// Read 32-bit value and extract the byte
	wordOffset := offset & 0xFC // Align to 4-byte boundary
	byteOffset := offset & 0x03 // Byte within word
	word := pciConfigRead32(bus, slot, funcNum, wordOffset)
	return uint8((word >> (byteOffset * 8)) & 0xFF)
}

// pciFindCapability finds a PCI capability by type
// Returns the offset of the capability, or 0 if not found
//
//go:nosplit
func pciFindCapability(bus, slot, funcNum uint8, capType uint8) uint8 {
	// Read capabilities pointer from offset 0x34
	capPtr := pciConfigRead8(bus, slot, funcNum, PCI_CAPABILITIES)

	// If capabilities pointer is 0 or 0xFF, no capabilities
	if capPtr == 0 || capPtr == 0xFF {
		return 0
	}

	// Traverse capability list
	// Each capability is at least 2 bytes: [type:8][next:8]
	maxIterations := 32 // Safety limit
	iterations := 0
	current := capPtr

	for current != 0 && iterations < maxIterations {
		// Read capability type (first byte)
		capTypeRead := pciConfigRead8(bus, slot, funcNum, current)

		if capTypeRead == capType {
			// Found it!
			return current
		}

		// Read next pointer (second byte)
		nextPtr := pciConfigRead8(bus, slot, funcNum, current+1)

		// If next is 0, we've reached the end
		if nextPtr == 0 {
			break
		}

		current = nextPtr
		iterations++
	}

	return 0 // Not found
}

// pciReadCapability reads a capability structure
// Returns the capability type and data
//
//go:nosplit
func pciReadCapability(bus, slot, funcNum, capOffset uint8) (capType uint8, data uint32) {
	// Read capability type
	capType = pciConfigRead8(bus, slot, funcNum, capOffset)

	// For VirtIO capabilities, read the full 32-bit capability structure
	// Format: [type:8][next:8][length:8][cfg_type:8]
	// Then device-specific data follows
	capData := pciConfigRead32(bus, slot, funcNum, capOffset)

	return capType, capData
}

// VirtIOCapabilityInfo holds information about a VirtIO PCI capability
type VirtIOCapabilityInfo struct {
	Offset      uint8  // Offset in PCI config space
	Type        uint8  // Capability type
	Bar         uint8  // BAR number (for Common Config, Notify, Device Config)
	OffsetInBar uint32 // Offset within BAR
	Length      uint32 // Length of capability region
}

// pciFindVirtIOCapabilities finds all VirtIO capabilities for a device
// Returns true if all required capabilities found
//
//go:nosplit
func pciFindVirtIOCapabilities(bus, slot, funcNum uint8, common, notify, isr, device *VirtIOCapabilityInfo) bool {
	// Find Common Config capability (required)
	commonOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_VENDOR_SPECIFIC)
	if commonOffset == 0 {
		uartPuts("PCI: VirtIO Common Config capability not found\r\n")
		return false
	}

	// Read Common Config capability structure
	// Format: [type:8][next:8][length:8][cfg_type:8][bar:8][padding:24]
	capData := pciConfigRead32(bus, slot, funcNum, commonOffset)
	barNum := uint8((capData >> 16) & 0xFF)
	offsetInBar := pciConfigRead32(bus, slot, funcNum, commonOffset+4) & 0xFFFFFFFC // Align to 4 bytes

	common.Offset = commonOffset
	common.Type = PCI_CAP_VENDOR_SPECIFIC
	common.Bar = barNum
	common.OffsetInBar = offsetInBar
	common.Length = 0x100 // Common config is typically 0x100 bytes

	uartPuts("PCI: Found VirtIO Common Config at offset 0x")
	printHex32(uint32(commonOffset))
	uartPuts(", BAR ")
	uartPutUint32(uint32(barNum))
	uartPuts(", offset in BAR 0x")
	printHex32(offsetInBar)
	uartPuts("\r\n")

	// Find Notify capability (required)
	notifyOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_NOTIFY)
	if notifyOffset == 0 {
		uartPuts("PCI: VirtIO Notify capability not found\r\n")
		return false
	}

	notifyCapData := pciConfigRead32(bus, slot, funcNum, notifyOffset)
	notifyBarNum := uint8((notifyCapData >> 16) & 0xFF)
	notifyOffsetInBar := pciConfigRead32(bus, slot, funcNum, notifyOffset+4) & 0xFFFFFFFC

	notify.Offset = notifyOffset
	notify.Type = PCI_CAP_NOTIFY
	notify.Bar = notifyBarNum
	notify.OffsetInBar = notifyOffsetInBar
	notify.Length = 0x100 // Notify config is typically 0x100 bytes

	uartPuts("PCI: Found VirtIO Notify at offset 0x")
	printHex32(uint32(notifyOffset))
	uartPuts(", BAR ")
	uartPutUint32(uint32(notifyBarNum))
	uartPuts(", offset in BAR 0x")
	printHex32(notifyOffsetInBar)
	uartPuts("\r\n")

	// Find ISR Status capability (optional but recommended)
	isrOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_ISR)
	if isrOffset != 0 {
		isrCapData := pciConfigRead32(bus, slot, funcNum, isrOffset)
		isrBarNum := uint8((isrCapData >> 16) & 0xFF)
		isrOffsetInBar := pciConfigRead32(bus, slot, funcNum, isrOffset+4) & 0xFFFFFFFC

		isr.Offset = isrOffset
		isr.Type = PCI_CAP_ISR
		isr.Bar = isrBarNum
		isr.OffsetInBar = isrOffsetInBar
		isr.Length = 4 // ISR is just one byte

		uartPuts("PCI: Found VirtIO ISR at offset 0x")
		printHex32(uint32(isrOffset))
		uartPuts(", BAR ")
		uartPutUint32(uint32(isrBarNum))
		uartPuts(", offset in BAR 0x")
		printHex32(isrOffsetInBar)
		uartPuts("\r\n")
	} else {
		uartPuts("PCI: VirtIO ISR capability not found (optional)\r\n")
	}

	// Find Device Config capability (optional, device-specific)
	deviceOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_DEVICE)
	if deviceOffset != 0 {
		deviceCapData := pciConfigRead32(bus, slot, funcNum, deviceOffset)
		deviceBarNum := uint8((deviceCapData >> 16) & 0xFF)
		deviceOffsetInBar := pciConfigRead32(bus, slot, funcNum, deviceOffset+4) & 0xFFFFFFFC

		device.Offset = deviceOffset
		device.Type = PCI_CAP_DEVICE
		device.Bar = deviceBarNum
		device.OffsetInBar = deviceOffsetInBar
		device.Length = 0x100 // Device config size varies

		uartPuts("PCI: Found VirtIO Device Config at offset 0x")
		printHex32(uint32(deviceOffset))
		uartPuts(", BAR ")
		uartPutUint32(uint32(deviceBarNum))
		uartPuts(", offset in BAR 0x")
		printHex32(deviceOffsetInBar)
		uartPuts("\r\n")
	} else {
		uartPuts("PCI: VirtIO Device Config capability not found (optional)\r\n")
	}

	return true
}
