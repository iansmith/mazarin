//go:build qemuvirt && aarch64

package main

import "mazboot/asm"

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
//
// NOTE: Our MMU maps both lowmem (0x3F000000-0x40000000) and highmem (0x4010000000+) ECAM windows.
// QEMU virt with virtualization=on typically uses highmem ECAM (0x4010000000).
// Default to highmem, fall back to lowmem if needed.
//
// This variable has an initializer, so Go places it in .noptrdata.
// The linker script places .noptrdata in RAM (writable) at 0x40100000.
var pciEcamBase uintptr = 0x4010000000

// pciFirstAccess tracks if this is the first PCI config space access (for debugging)
var pciFirstAccess bool = true

// pciConfigRead32 reads a 32-bit value from PCI configuration space
//
//go:nosplit
func pciConfigRead32(bus, slot, funcNum, offset uint8) uint32 {
	if pciEcamBase == 0 {
		return 0xFFFFFFFF
	}

	configAddr := pciEcamBase +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)

	// Validate address range
	isLowmem := configAddr >= 0x3F000000 && configAddr < 0x40000000
	isHighmem := configAddr >= 0x4010000000
	if !isLowmem && !isHighmem {
		return 0xFFFFFFFF
	}

	pciFirstAccess = false
	asm.Dsb()
	asm.Isb()
	value := asm.MmioRead(configAddr)
	asm.Dsb()
	return value
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
	asm.MmioWrite(configAddr, value)
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
	return asm.MmioRead(configAddr)
}

// findBochsDisplay finds the bochs-display PCI device and returns its BAR0 address
//
//go:nosplit
func findBochsDisplay() uintptr {
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				vendorID := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID) & 0xFFFF
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}
				deviceID := pciConfigRead32(bus, slot, funcNum, PCI_DEVICE_ID) & 0xFFFF
				if vendorID == BOCHS_VENDOR_ID && deviceID == BOCHS_DEVICE_ID {
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					return uintptr(bar0 & 0xFFFFFFF0)
				}
			}
		}
	}
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
//
//go:nosplit
func findBochsDisplayFull() bool {
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				fullReg := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID)
				vendorIDActual := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				if vendorIDActual == 0xFFFF || vendorIDActual == 0 {
					continue
				}

				if vendorIDActual == BOCHS_VENDOR_ID && deviceID == BOCHS_DEVICE_ID {
					// Enable device (I/O, memory, bus master)
					cmd := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
					cmd |= 0x7
					pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, cmd)
					for delay := 0; delay < 1000; delay++ {
					}

					// Probe BARs
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, 0xFFFFFFFF)
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, 0xFFFFFFFF)
					pciConfigRead32(bus, slot, funcNum, PCI_BAR0) // size mask
					pciConfigRead32(bus, slot, funcNum, PCI_BAR2) // size mask

					// Program BAR addresses (PCI MMIO window at 0x10000000)
					fbAddr := uintptr(0x10000000)
					mmioBase := uintptr(0x10F00000)
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR0, uint32(fbAddr)|0x8)
					pciConfigWrite32(bus, slot, funcNum, PCI_BAR2, uint32(mmioBase))
					for delay := 0; delay < 1000; delay++ {
					}

					// Read back and validate
					bar0 := pciConfigRead32(bus, slot, funcNum, PCI_BAR0)
					bar2 := pciConfigRead32(bus, slot, funcNum, PCI_BAR2)
					if bar0 == 0xFFFFFFFF || bar0 == 0 || bar2 == 0xFFFFFFFF || bar2 == 0 {
						return false
					}

					fbAddr = uintptr(bar0 & 0xFFFFFFF0)
					mmioBase = uintptr(bar2 & 0xFFFFFFF0)

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
	return false
}

// Note: printHex32 moved to kernel.go as a global function

// VBE register base offset from MMIO base
// For Bochs/QEMU, the MMIO window at BAR2 contains the VBE_DISPI registers
// laid out as a contiguous array of 16-bit registers:
//
//	MMIO base + 0x500 + (index << 1)
//
// This matches QEMU's BOCHS_VBE_DISPI_MMIO_INDEX definition; the separate
// index/data pair exists only for the legacy I/O port interface (0x1CE/0x1CF),
// not for this MMIO BAR.
const (
	VBE_DISPI_REG_BASE_OFFSET = 0x500 // Base offset for VBE registers
)

// writeVBERegister writes a 16-bit value directly to a VBE "dispi" register
// using the MMIO layout MMIO base + 0x500 + (index << 1).
//
//go:nosplit
func writeVBERegister(index, value uint16) {
	regAddr := bochsDisplayInfo.MMIOBase + VBE_DISPI_REG_BASE_OFFSET + uintptr(index<<1)
	asm.MmioWrite16(regAddr, value)
	asm.Dsb() // Ensure write completes
}

// readVBERegister reads a 16-bit value directly from a VBE "dispi" register
// using the MMIO layout MMIO base + 0x500 + (index << 1).
//
//go:nosplit
func readVBERegister(index uint16) uint16 {
	regAddr := bochsDisplayInfo.MMIOBase + VBE_DISPI_REG_BASE_OFFSET + uintptr(index<<1)
	val := asm.MmioRead16(regAddr)
	asm.Dsb() // Barrier after read
	return val
}

// initBochsDisplay initializes the bochs-display device via VBE registers
//
//go:nosplit
func initBochsDisplay(width, height, bpp uint16) bool {
	mmioBase := bochsDisplayInfo.MMIOBase
	if mmioBase == 0 {
		return false
	}

	// Set framebuffer endianness if needed
	endiannessAddr := mmioBase + QEMU_EXT_REG_ENDIANNESS
	endianness := asm.MmioRead(endiannessAddr)
	if endianness != QEMU_ENDIANNESS_LITTLE {
		asm.MmioWrite(endiannessAddr, QEMU_ENDIANNESS_LITTLE)
		asm.Dsb()
	}

	// 1. Disable VBE extensions
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, VBE_DISPI_DISABLED)

	// 2. Set resolution and color depth
	writeVBERegister(VBE_DISPI_INDEX_XRES, width)
	writeVBERegister(VBE_DISPI_INDEX_YRES, height)
	writeVBERegister(VBE_DISPI_INDEX_BPP, bpp)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_WIDTH, width)
	writeVBERegister(VBE_DISPI_INDEX_VIRT_HEIGHT, height)
	writeVBERegister(VBE_DISPI_INDEX_X_OFFSET, 0)
	writeVBERegister(VBE_DISPI_INDEX_Y_OFFSET, 0)

	// 3. Enable VBE extensions with LFB
	enableValue := uint16(VBE_DISPI_ENABLED | VBE_DISPI_LFB_ENABLED)
	writeVBERegister(VBE_DISPI_INDEX_ENABLE, enableValue)

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
//
//go:nosplit
func pciFindVirtIOCapabilities(bus, slot, funcNum uint8, common, notify, isr, device *VirtIOCapabilityInfo) bool {
	// Find Common Config capability (required)
	commonOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_VENDOR_SPECIFIC)
	if commonOffset == 0 {
		return false
	}
	capData := pciConfigRead32(bus, slot, funcNum, commonOffset)
	common.Offset = commonOffset
	common.Type = PCI_CAP_VENDOR_SPECIFIC
	common.Bar = uint8((capData >> 16) & 0xFF)
	common.OffsetInBar = pciConfigRead32(bus, slot, funcNum, commonOffset+4) & 0xFFFFFFFC
	common.Length = 0x100

	// Find Notify capability (required)
	notifyOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_NOTIFY)
	if notifyOffset == 0 {
		return false
	}
	notifyCapData := pciConfigRead32(bus, slot, funcNum, notifyOffset)
	notify.Offset = notifyOffset
	notify.Type = PCI_CAP_NOTIFY
	notify.Bar = uint8((notifyCapData >> 16) & 0xFF)
	notify.OffsetInBar = pciConfigRead32(bus, slot, funcNum, notifyOffset+4) & 0xFFFFFFFC
	notify.Length = 0x100

	// Find ISR Status capability (optional)
	isrOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_ISR)
	if isrOffset != 0 {
		isrCapData := pciConfigRead32(bus, slot, funcNum, isrOffset)
		isr.Offset = isrOffset
		isr.Type = PCI_CAP_ISR
		isr.Bar = uint8((isrCapData >> 16) & 0xFF)
		isr.OffsetInBar = pciConfigRead32(bus, slot, funcNum, isrOffset+4) & 0xFFFFFFFC
		isr.Length = 4
	}

	// Find Device Config capability (optional)
	deviceOffset := pciFindCapability(bus, slot, funcNum, PCI_CAP_DEVICE)
	if deviceOffset != 0 {
		deviceCapData := pciConfigRead32(bus, slot, funcNum, deviceOffset)
		device.Offset = deviceOffset
		device.Type = PCI_CAP_DEVICE
		device.Bar = uint8((deviceCapData >> 16) & 0xFF)
		device.OffsetInBar = pciConfigRead32(bus, slot, funcNum, deviceOffset+4) & 0xFFFFFFFC
		device.Length = 0x100
	}

	return true
}
