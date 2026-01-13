//go:build qemuvirt && aarch64

package dtb

import (
	"unsafe"
)

// Device types we support
const (
	DeviceTypeGIC = iota
	DeviceTypePL011
	DeviceTypeVirtIOMmio
)

// DeviceMatch describes a device we can recognize from DTB
type DeviceMatch struct {
	Compatible string
	DeviceType int
}

// getSupportedDevices returns the list of devices we can recognize
// Using a function instead of global to avoid initialization issues
func getSupportedDevices() []DeviceMatch {
	return []DeviceMatch{
		{Compatible: "arm,gic-400", DeviceType: DeviceTypeGIC},
		{Compatible: "arm,cortex-a15-gic", DeviceType: DeviceTypeGIC},
		{Compatible: "arm,gic-v2m", DeviceType: DeviceTypeGIC},
		{Compatible: "arm,pl011", DeviceType: DeviceTypePL011},
		{Compatible: "virtio,mmio", DeviceType: DeviceTypeVirtIOMmio},
	}
}

// DiscoveredDevice holds information about a device found in DTB
type DiscoveredDevice struct {
	DeviceType int
	Name       [64]byte
	NameLen    int
	BaseAddr   uintptr
	Size       uint64
	IRQ        int
	// For GIC, we need both GICD and GICC
	GICDAddr   uintptr
	GICCAddr   uintptr
	// For VirtIO, device ID from MMIO register
	VirtIODeviceID uint32
}

// Use a slice instead of array - let Go runtime allocate as needed
var discoveredDevices []DiscoveredDevice

// DiscoverAndInitDevices discovers devices from DTB and initializes them
// This is the main entry point called from kmazarin main
//
func DiscoverAndInitDevices(dtbAddr uintptr) bool {
	// Phase 1: Discover all devices
	discoveredDevices = make([]DiscoveredDevice, 0, 16)

	success := Walk(dtbAddr, discoverDeviceCallback)
	if !success {
		return false
	}

	// Phase 2: Initialize in dependency order
	initializeDevices()

	return true
}

// discoverDeviceCallback is called for each node during DTB walk
//
func discoverDeviceCallback(node *DTBNode) {
	// Check if this node matches any supported device
	supportedDevices := getSupportedDevices()
	for _, match := range supportedDevices {
		if node.HasCompatible(match.Compatible) {
			var dev DiscoveredDevice
			dev.DeviceType = match.DeviceType

			// Copy name
			var nameBuf [64]byte
			nameLen := node.GetNameBuf(nameBuf[:])
			dev.NameLen = nameLen
			if nameLen > len(dev.Name) {
				nameLen = len(dev.Name)
			}
			for i := 0; i < nameLen; i++ {
				dev.Name[i] = nameBuf[i]
			}

			// Extract device-specific information
			switch match.DeviceType {
			case DeviceTypeGIC:
				// GIC has multiple reg entries: GICD and GICC
				var regs [4][2]uint64
				numRegs := node.GetRegMulti(&regs)
				if numRegs >= 2 {
					dev.GICDAddr = uintptr(regs[0][0])
					dev.GICCAddr = uintptr(regs[1][0])
				}

			case DeviceTypePL011, DeviceTypeVirtIOMmio:
				// Single reg entry
				addr, size, ok := node.GetReg()
				if ok {
					dev.BaseAddr = addr
					dev.Size = size
				}

				// Get IRQ
				irq, ok := node.GetInterrupt()
				if ok {
					dev.IRQ = irq
				}
			}

			discoveredDevices = append(discoveredDevices, dev)
			break // Found a match, move to next node
		}
	}
}

// initializeDevices initializes discovered devices in correct dependency order
//
func initializeDevices() {
	// 1. GIC first (everything needs interrupts)
	for i := range discoveredDevices {
		dev := &discoveredDevices[i]
		if dev.DeviceType == DeviceTypeGIC {
			initGIC(dev)
			break
		}
	}

	// 2. PL011 Serial (can now enable IRQs since GIC is ready)
	for i := range discoveredDevices {
		dev := &discoveredDevices[i]
		if dev.DeviceType == DeviceTypePL011 {
			initPL011Serial(dev)
			break
		}
	}

	// 3. VirtIO devices
	for i := range discoveredDevices {
		dev := &discoveredDevices[i]
		if dev.DeviceType == DeviceTypeVirtIOMmio {
			initVirtIODevice(dev)
		}
	}
}

// initGIC initializes the GIC (Generic Interrupt Controller)
//
func initGIC(dev *DiscoveredDevice) {
	// TODO: Implement GIC initialization
	// For now, just record that we found it
	// GIC init will:
	// 1. Initialize GICD (distributor)
	// 2. Initialize GICC (CPU interface)
	// 3. Enable interrupt routing
	_ = dev // Suppress unused warning
}

// initPL011Serial initializes the PL011 UART with ring buffer + IRQ
//
func initPL011Serial(dev *DiscoveredDevice) {
	// TODO: Initialize PL011 serial device
	// For now, just record that we found it
	_ = dev // Suppress unused warning
}

// initVirtIODevice initializes a VirtIO MMIO device
//
func initVirtIODevice(dev *DiscoveredDevice) {
	// Read VirtIO device type from MMIO register
	// Offset 0x08: DeviceID
	deviceID := readMMIO32(dev.BaseAddr + 0x08)
	dev.VirtIODeviceID = deviceID

	switch deviceID {
	case 4: // VirtIO RNG
		initVirtIORNG(dev)
	case 1: // VirtIO Network
		// Not supported yet
	default:
		// Unknown VirtIO device
	}
}

// initVirtIORNG initializes the VirtIO RNG device
//
func initVirtIORNG(dev *DiscoveredDevice) {
	// TODO: Initialize VirtIO RNG device
	// For now, just record that we found it
	_ = dev // Suppress unused warning
}

// mmio_read32 is implemented in mmio_arm64.s
// Reads a 32-bit value from MMIO using volatile memory access
//
//go:nosplit
func mmio_read32(addr uintptr) uint32

// mmio_write32 is implemented in mmio_arm64.s
// Writes a 32-bit value to MMIO using volatile memory access
//
//go:nosplit
func mmio_write32(addr uintptr, val uint32)

// readMMIO32 reads a 32-bit value from MMIO
// Uses assembly volatile read to prevent compiler optimization
//
//go:nosplit
func readMMIO32(addr uintptr) uint32 {
	return mmio_read32(addr)
}

// GetDiscoveredDevices returns the list of discovered devices
// Useful for debugging and verification
//
func GetDiscoveredDevices() []DiscoveredDevice {
	return discoveredDevices
}
