// pci_constants.go — Shared VirtIO PCI transport constants.
//
// These are the canonical values for VirtIO device status bits and PCI common
// configuration register offsets. Previously duplicated across block, gpu,
// input, and rng packages.
//
// Reference: VirtIO Specification 1.2, sections 2.1 (Device Status Field)
// and 4.1.4.3 (Common configuration structure layout).

package virtio

// VirtIO Device Status Bits (spec section 2.1)
//
// Values match QEMU's virtio.h VIRTIO_CONFIG_S_* defines.
const (
	StatusAcknowledge    = 1   // 1 << 0: Guest OS has found the device
	StatusDriver         = 2   // 1 << 1: Guest OS knows how to drive the device
	StatusDriverOK       = 4   // 1 << 2: Driver is set up and ready to drive the device
	StatusFeaturesOK     = 8   // 1 << 3: Driver has acknowledged all features it understands
	StatusDeviceNeedsReset = 64  // 1 << 6: Device has experienced an error (read-only)
	StatusFailed         = 128 // 1 << 7: Something went wrong; device gave up on guest
)

// VirtIO PCI Common Configuration Register Offsets (spec section 4.1.4.3)
//
// These offsets are relative to the BAR + offset indicated by the
// VIRTIO_PCI_CAP_COMMON_CFG capability structure.
const (
	CfgDeviceFeatureSelect = 0x00 // RW: Select device feature bits (0=low 32, 1=high 32)
	CfgDeviceFeature       = 0x04 // RO: Device feature bits (selected page)
	CfgDriverFeatureSelect = 0x08 // RW: Select driver feature bits (0=low 32, 1=high 32)
	CfgDriverFeature       = 0x0C // RW: Driver feature bits (selected page)
	CfgMSIXConfig          = 0x10 // RW: MSI-X configuration vector
	CfgNumQueues           = 0x12 // RO: Maximum number of virtqueues
	CfgDeviceStatus        = 0x14 // RW: Device status (8-bit)
	CfgConfigGeneration    = 0x15 // RO: Configuration atomicity value
	CfgQueueSelect         = 0x16 // RW: Select virtqueue (write index before accessing queue fields)
	CfgQueueSize           = 0x18 // RW: Queue size (max or actual, depending on direction)
	CfgQueueMSIXVector     = 0x1A // RW: Queue MSI-X vector
	CfgQueueEnable         = 0x1C // RW: Queue enable
	CfgQueueNotifyOff      = 0x1E // RO: Queue notification offset (multiplied by notify_off_multiplier)
	CfgQueueDescLow        = 0x20 // RW: Queue descriptor table address (low 32)
	CfgQueueDescHigh       = 0x24 // RW: Queue descriptor table address (high 32)
	CfgQueueAvailLow       = 0x28 // RW: Queue available ring address (low 32)
	CfgQueueAvailHigh      = 0x2C // RW: Queue available ring address (high 32)
	CfgQueueUsedLow        = 0x30 // RW: Queue used ring address (low 32)
	CfgQueueUsedHigh       = 0x34 // RW: Queue used ring address (high 32)
)

// VirtIO Feature Bits
const (
	FeatureVersion1 = 1 << 0 // Bit 32 (in high feature page): VirtIO 1.0+ compliant
)

// MSI-X vector value indicating no vector assigned
const MSIXNoVector = 0xFFFF

// PCI Vendor ID for VirtIO devices
const VirtIOVendorID = 0x1AF4
