package pci

import "mazzy/kmazarin/asm"

// PCI ECAM config-space access and VirtIO capability discovery for the QEMU q35
// (x86_64) and virt (ARM64) machines. The arch-specific config read/write live in
// ecam_amd64.go / ecam_arm64.go.

// PCI configuration space constants
const (
	PCI_CONFIG_ADDRESS = 0x0CF8 // I/O port for PCI config address
	PCI_CONFIG_DATA    = 0x0CFC // I/O port for PCI config data

	// VirtIO device IDs
	VIRTIO_VENDOR_ID     = 0x1AF4
	VIRTIO_GPU_DEVICE_ID = 0x1050

	// PCI configuration space offsets
	PCI_VENDOR_ID    = 0x00
	PCI_DEVICE_ID    = 0x02
	PCI_COMMAND      = 0x04 // Command register - bit 0 = I/O enable, bit 1 = memory enable
	PCI_CAPABILITIES = 0x34 // Capabilities pointer (offset to first capability)
)

// PCI capability types
const (
	PCI_CAP_VENDOR_SPECIFIC = 0x09 // PCI Vendor-Specific capability type

	// VirtIO cfg_type values (within vendor-specific capabilities)
	VIRTIO_PCI_CAP_COMMON_CFG = 1 // Common configuration
	VIRTIO_PCI_CAP_NOTIFY_CFG = 2 // Notifications
	VIRTIO_PCI_CAP_ISR_CFG    = 3 // ISR Status
	VIRTIO_PCI_CAP_DEVICE_CFG = 4 // Device-specific configuration
	VIRTIO_PCI_CAP_PCI_CFG    = 5 // PCI configuration access
)

// pciEcamBase is the PCI ECAM base address (arch-specific, see ecam_arm64.go / ecam_amd64.go)

// pciFirstAccess tracks if this is the first PCI config space access (for debugging)
var pciFirstAccess bool = true

// GetEcamBase returns the PCI ECAM base address for debugging
func GetEcamBase() uintptr { return pciEcamBase }

// ConfigRead32 and ConfigWrite32 are arch-specific:
// - ARM64: ECAM MMIO access (ecam_arm64.go)
// - x86_64: I/O port CF8/CFC access (ecam_amd64.go)

// ConfigRead32Lowmem reads using lowmem ECAM address (for testing)
//
//go:nosplit
func ConfigRead32Lowmem(bus, slot, funcNum, offset uint8) uint32 {
	pciEcamBaseLow := uintptr(0x3F000000)
	configAddr := pciEcamBaseLow +
		uintptr(bus)<<20 +
		uintptr(slot)<<15 +
		uintptr(funcNum)<<12 +
		uintptr(offset&0xFC)
	return asm.MmioRead(configAddr)
}

// ReadBAR64 reads a PCI BAR, handling both 32-bit and 64-bit BARs.
// For 64-bit BARs (type indicator bits [2:1] == 0b10), reads both the low
// and high 32-bit registers to construct the full physical address.
// Returns the base address with type/prefetchable bits masked off.
//
//go:nosplit
func ReadBAR64(bus, slot, funcNum, barIndex uint8) uintptr {
	barOffset := 0x10 + barIndex*4
	barLow := ConfigRead32(bus, slot, funcNum, barOffset)

	addr := uintptr(barLow & 0xFFFFFFF0)

	// Check if 64-bit BAR (type bits [2:1] == 0b10)
	if (barLow & 0x6) == 0x4 {
		barHigh := ConfigRead32(bus, slot, funcNum, barOffset+4)
		addr |= uintptr(barHigh) << 32
	}

	return addr
}

// WriteBAR64 writes a PCI BAR address, handling both 32-bit and 64-bit BARs.
// For 64-bit BARs, writes both low and high 32-bit registers.
//
//go:nosplit
func WriteBAR64(bus, slot, funcNum, barIndex uint8, addr uintptr) {
	barOffset := 0x10 + barIndex*4

	// Read current BAR to check type
	barLow := ConfigRead32(bus, slot, funcNum, barOffset)
	typeBits := barLow & 0xF // preserve type/prefetch bits

	ConfigWrite32(bus, slot, funcNum, barOffset, uint32(addr)|typeBits)

	// Write high 32 bits if 64-bit BAR
	if (barLow & 0x6) == 0x4 {
		ConfigWrite32(bus, slot, funcNum, barOffset+4, uint32(addr>>32))
	}
}

// PCI Capability Reading Functions

// ConfigRead8 reads an 8-bit value from PCI configuration space
//
//go:nosplit
func ConfigRead8(bus, slot, funcNum, offset uint8) uint8 {
	// Read 32-bit value and extract the byte
	wordOffset := offset & 0xFC // Align to 4-byte boundary
	byteOffset := offset & 0x03 // Byte within word
	word := ConfigRead32(bus, slot, funcNum, wordOffset)
	return uint8((word >> (byteOffset * 8)) & 0xFF)
}

// pciFindCapability finds a PCI capability by type
// Returns the offset of the capability, or 0 if not found
//
//go:nosplit
func pciFindCapability(bus, slot, funcNum uint8, capType uint8) uint8 {
	// Read capabilities pointer from offset 0x34
	capPtr := ConfigRead8(bus, slot, funcNum, PCI_CAPABILITIES)

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
		capTypeRead := ConfigRead8(bus, slot, funcNum, current)

		if capTypeRead == capType {
			// Found it!
			return current
		}

		// Read next pointer (second byte)
		nextPtr := ConfigRead8(bus, slot, funcNum, current+1)

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
	capType = ConfigRead8(bus, slot, funcNum, capOffset)

	// For VirtIO capabilities, read the full 32-bit capability structure
	// Format: [type:8][next:8][length:8][cfg_type:8]
	// Then device-specific data follows
	capData := ConfigRead32(bus, slot, funcNum, capOffset)

	return capType, capData
}

// VirtIOCapabilityInfo holds information about a VirtIO PCI capability
type VirtIOCapabilityInfo struct {
	Offset              uint8  // Offset in PCI config space
	Type                uint8  // Capability type
	Bar                 uint8  // BAR number (for Common Config, Notify, Device Config)
	OffsetInBar         uint32 // Offset within BAR
	Length              uint32 // Length of capability region
	NotifyOffMultiplier uint32 // Notify capability only: multiplier for queue_notify_off
}

// FindVirtIOCapability finds a VirtIO capability by cfg_type
// Returns the offset of the capability, or 0 if not found
//
//go:nosplit
func FindVirtIOCapability(bus, slot, funcNum uint8, cfgType uint8) uint8 {
	// Read capabilities pointer
	capPtr := ConfigRead8(bus, slot, funcNum, PCI_CAPABILITIES)
	if capPtr == 0 || capPtr == 0xFF {
		return 0
	}

	// Traverse capability list looking for vendor-specific (0x09) caps
	current := capPtr
	for i := 0; i < 32 && current != 0; i++ {
		capTypeRead := ConfigRead8(bus, slot, funcNum, current)

		if capTypeRead == PCI_CAP_VENDOR_SPECIFIC {
			// Check cfg_type field (offset +3)
			virtCfgType := ConfigRead8(bus, slot, funcNum, current+3)
			if virtCfgType == cfgType {
				return current
			}
		}

		// Move to next capability
		current = ConfigRead8(bus, slot, funcNum, current+1)
	}

	return 0
}

// pciFindVirtIOCapabilities finds all VirtIO capabilities for a device
//
//go:nosplit
func FindVirtIOCapabilities(bus, slot, funcNum uint8, common, notify, isr, device *VirtIOCapabilityInfo) bool {
	// Find Common Config capability (required)
	commonOffset := FindVirtIOCapability(bus, slot, funcNum, VIRTIO_PCI_CAP_COMMON_CFG)
	if commonOffset == 0 {
		return false
	}
	common.Offset = commonOffset
	common.Type = VIRTIO_PCI_CAP_COMMON_CFG
	common.Bar = ConfigRead8(bus, slot, funcNum, commonOffset+4)
	common.OffsetInBar = ConfigRead32(bus, slot, funcNum, commonOffset+8)
	common.Length = ConfigRead32(bus, slot, funcNum, commonOffset+12)

	// Find Notify capability (required)
	notifyOffset := FindVirtIOCapability(bus, slot, funcNum, VIRTIO_PCI_CAP_NOTIFY_CFG)
	if notifyOffset == 0 {
		return false
	}
	notify.Offset = notifyOffset
	notify.Type = VIRTIO_PCI_CAP_NOTIFY_CFG
	notify.Bar = ConfigRead8(bus, slot, funcNum, notifyOffset+4)
	notify.OffsetInBar = ConfigRead32(bus, slot, funcNum, notifyOffset+8)
	notify.Length = ConfigRead32(bus, slot, funcNum, notifyOffset+12)
	notify.NotifyOffMultiplier = ConfigRead32(bus, slot, funcNum, notifyOffset+16)

	// Find ISR Status capability (optional)
	isrOffset := FindVirtIOCapability(bus, slot, funcNum, VIRTIO_PCI_CAP_ISR_CFG)
	if isrOffset != 0 {
		isr.Offset = isrOffset
		isr.Type = VIRTIO_PCI_CAP_ISR_CFG
		isr.Bar = ConfigRead8(bus, slot, funcNum, isrOffset+4)
		isr.OffsetInBar = ConfigRead32(bus, slot, funcNum, isrOffset+8)
		isr.Length = ConfigRead32(bus, slot, funcNum, isrOffset+12)
	}

	// Find Device Config capability (optional)
	deviceOffset := FindVirtIOCapability(bus, slot, funcNum, VIRTIO_PCI_CAP_DEVICE_CFG)
	if deviceOffset != 0 {
		device.Offset = deviceOffset
		device.Type = VIRTIO_PCI_CAP_DEVICE_CFG
		device.Bar = ConfigRead8(bus, slot, funcNum, deviceOffset+4)
		device.OffsetInBar = ConfigRead32(bus, slot, funcNum, deviceOffset+8)
		device.Length = ConfigRead32(bus, slot, funcNum, deviceOffset+12)
	}

	return true
}
