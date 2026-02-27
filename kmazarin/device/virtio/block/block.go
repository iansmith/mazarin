package block

import (
	"unsafe"

	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
)

// VirtIO PCI constants
const (
	VIRTIO_STATUS_ACKNOWLEDGE = 1
	VIRTIO_STATUS_DRIVER      = 2
	VIRTIO_STATUS_DRIVER_OK   = 4
	VIRTIO_STATUS_FEATURES_OK = 8
	VIRTIO_STATUS_FAILED      = 128
)

// VirtIO PCI common config offsets
const (
	VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT = 0x00
	VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE        = 0x04
	VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT = 0x08
	VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE        = 0x0C
	VIRTIO_PCI_COMMON_CFG_MSIX_CONFIG           = 0x10
	VIRTIO_PCI_COMMON_CFG_NUM_QUEUES            = 0x12
	VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS         = 0x14
	VIRTIO_PCI_COMMON_CFG_CONFIG_GENERATION     = 0x15
	VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT          = 0x16
	VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE            = 0x18
	VIRTIO_PCI_COMMON_CFG_QUEUE_MSIX_VECTOR     = 0x1A
	VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE          = 0x1C
	VIRTIO_PCI_COMMON_CFG_QUEUE_NOTIFY_OFF      = 0x1E
	VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW        = 0x20
	VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH       = 0x24
	VIRTIO_PCI_COMMON_CFG_QUEUE_DRIVER_LOW      = 0x28
	VIRTIO_PCI_COMMON_CFG_QUEUE_DRIVER_HIGH     = 0x2C
	VIRTIO_PCI_COMMON_CFG_QUEUE_DEVICE_LOW      = 0x30
	VIRTIO_PCI_COMMON_CFG_QUEUE_DEVICE_HIGH     = 0x34
)

// VirtIO block PCI device IDs
const (
	VIRTIO_BLK_DEVICE_ID_LEGACY = 0x1001 // Transitional device
	VIRTIO_BLK_DEVICE_ID_MODERN = 0x1042 // Non-transitional (VirtIO 1.0+)
)

// VirtIOBlockDevice represents a VirtIO block device
type VirtIOBlockDevice struct {
	// PCI location
	Bus, Slot, Func uint8

	// VirtIO PCI capability info
	CommonConfig pci.VirtIOCapabilityInfo
	NotifyConfig pci.VirtIOCapabilityInfo
	ISRConfig    pci.VirtIOCapabilityInfo
	DeviceConfig pci.VirtIOCapabilityInfo

	// Mapped addresses
	CommonConfigBase uintptr // Common config BAR mapped address
	NotifyBase       uintptr // Notify BAR mapped address

	// VirtQueue for I/O
	Queue           virtio.VirtQueue
	QueueNotifyOff  uint16 // Notify offset for I/O queue

	// Device configuration
	Capacity      uint64 // Total sectors
	BlockSizeBytes uint32 // Bytes per sector (typically 512)

	// MMIO transport (RISC-V): non-zero means MMIO mode, zero means PCI mode
	MMIOBase uintptr

	// DMA-safe I/O buffer: pre-mapped physical memory avoids demand paging issues.
	// Layout (within one 4KB page):
	//   [0..15]   VirtIOBlockReq header
	//   [16..16]  Status byte
	//   [32..543] 512-byte data buffer (aligned to 32 for safety)
	DmaPagePA uintptr // Physical address of the DMA page
	DmaPageVA uintptr // Virtual address of the DMA page (PA + KernelMMIOOffset)
}

// Global device instance
var virtioBlockDevice VirtIOBlockDevice

// Init initializes the VirtIO block device.
// Tries PCI first (works on all architectures), falls back to MMIO (RISC-V).
// Returns true on success, false if no device found or initialization failed.
func Init() bool {
	// Try PCI first (all architectures)
	if findVirtIOBlock() {
		// PCI path
		if !virtioBlockInit() {
			console.KPrintln("[VirtIO Block] PCI device initialization failed")
			return false
		}
		if !virtioBlockReadConfig() {
			console.KPrintln("[VirtIO Block] PCI failed to read config")
			return false
		}
	} else {
		// PCI not found — try MMIO (RISC-V)
		if !findVirtIOBlockMMIO() {
			console.KPrintln("[VirtIO Block] No block device found (PCI or MMIO)")
			return false
		}
		if !virtioBlockInitMMIO() {
			console.KPrintln("[VirtIO Block] MMIO device initialization failed")
			return false
		}
		if !virtioBlockReadConfigMMIO() {
			console.KPrintln("[VirtIO Block] MMIO failed to read config")
			return false
		}
	}

	// Allocate a DMA-safe page for I/O buffers.
	// This avoids demand paging issues — the page has a known PA via the linear map.
	dmaPA := kmem.AllocKernelFrame()
	if dmaPA == 0 {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate DMA page")
		return false
	}
	virtioBlockDevice.DmaPagePA = dmaPA
	virtioBlockDevice.DmaPageVA = dmaPA + constants.KernelMMIOOffset

	console.KPrintf("[VirtIO Block] Initialized: %d MB (%d sectors)\n",
		virtioBlockDevice.Capacity/2048, virtioBlockDevice.Capacity)

	// Register with device manager
	device.RegisterBlockDevice(&virtioBlockDevice)

	return true
}

// findVirtIOBlock scans the PCI bus for a VirtIO block device
func findVirtIOBlock() bool {
	// PCI bus scan (silent)

	// Scan PCI bus
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			// Read func 0 first to check if device exists and if multi-function
			fullReg0 := pci.ConfigRead32(bus, slot, 0, pci.PCI_VENDOR_ID)
			vendorID0 := fullReg0 & 0xFFFF
			if vendorID0 == 0xFFFF || vendorID0 == 0 {
				continue // No device at this slot
			}

			// Check multi-function bit in header type register
			headerType := pci.ConfigRead32(bus, slot, 0, 0x0C)
			isMultiFunc := (headerType>>16)&0x80 != 0
			maxFunc := uint8(1)
			if isMultiFunc {
				maxFunc = 8
			}

			for funcNum := uint8(0); funcNum < maxFunc; funcNum++ {
				var fullReg uint32
				if funcNum == 0 {
					fullReg = fullReg0 // Already read above
				} else {
					fullReg = pci.ConfigRead32(bus, slot, funcNum, pci.PCI_VENDOR_ID)
				}
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				// Check if device exists
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Check if this is VirtIO block device
				if vendorID == pci.VIRTIO_VENDOR_ID &&
					(deviceID == VIRTIO_BLK_DEVICE_ID_LEGACY || deviceID == VIRTIO_BLK_DEVICE_ID_MODERN) {
					// Enable device
					cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
					cmd |= 0x7 // Enable I/O, memory, bus master
					pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

					// Find VirtIO capabilities
					var common, notify, isr, deviceCfg pci.VirtIOCapabilityInfo
					if !pci.FindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &deviceCfg) {
						console.KPrintln("[VirtIO Block] ERROR: VirtIO capabilities not found")
						return false
					}

					// Read BAR for common config (handles 64-bit BARs)
					barBase := pci.ReadBAR64(bus, slot, funcNum, common.Bar)

					// If BAR is zero OR above 4GB (can't map via KernelMMIOOffset),
					// reprogram to the 32-bit PCI MMIO window.
					// Use PCI_MMIO_BASE + 0x200000 to avoid collision with GPU BARs.
					const blockMMIOBase = pci.PCI_MMIO_BASE + 0x200000
					if barBase == 0 || barBase >= 0x100000000 {
						pci.WriteBAR64(bus, slot, funcNum, common.Bar, blockMMIOBase)
						barBase = pci.ReadBAR64(bus, slot, funcNum, common.Bar)
					}

					// Map BAR MMIO into kernel high-memory (TTBR1)
					kmem.MapDeviceMMIO(barBase, 0x10000)

					virtioBlockDevice.Bus = bus
					virtioBlockDevice.Slot = slot
					virtioBlockDevice.Func = funcNum
					virtioBlockDevice.CommonConfig = common
					virtioBlockDevice.NotifyConfig = notify
					virtioBlockDevice.ISRConfig = isr
					virtioBlockDevice.DeviceConfig = deviceCfg
					virtioBlockDevice.CommonConfigBase = barBase + constants.KernelMMIOOffset + uintptr(common.OffsetInBar)

					// Calculate notify base (handles 64-bit BARs)
					notifyBarBase := pci.ReadBAR64(bus, slot, funcNum, notify.Bar)
					if notifyBarBase == 0 || notifyBarBase >= 0x100000000 {
						pci.WriteBAR64(bus, slot, funcNum, notify.Bar, blockMMIOBase+0x10000)
						notifyBarBase = pci.ReadBAR64(bus, slot, funcNum, notify.Bar)
					}
					if notifyBarBase != barBase {
						kmem.MapDeviceMMIO(notifyBarBase, 0x10000)
					}
					virtioBlockDevice.NotifyBase = notifyBarBase + constants.KernelMMIOOffset + uintptr(notify.OffsetInBar)

					return true
				}
			}
		}
	}

	return false
}

// virtioBlockInit initializes the VirtIO block device
// Returns true on success, false on failure
//
//go:nosplit
func virtioBlockInit() bool {
	// Reset device
	virtioPCISetDeviceStatus(0)

	// Acknowledge and indicate driver present
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE)
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER)

	// Feature negotiation - read device features
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 0)
	virtioPCIReadCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 1)
	virtioPCIReadCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)

	// Accept VIRTIO_F_VERSION_1 (bit 32)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 0)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 0)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 1)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 1) // VIRTIO_F_VERSION_1

	// Features OK
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK)

	// Verify FEATURES_OK is still set
	status := virtioPCIGetDeviceStatus()
	if (status & VIRTIO_STATUS_FEATURES_OK) == 0 {
		console.KPrintln("[VirtIO Block] ERROR: Device rejected features")
		return false
	}

	// Allocate a DMA page for virtqueue structures (desc table, avail ring, used ring).
	// Using a pre-allocated page with known PA avoids demand-paging page table walks
	// for the queue structures themselves.
	queuePagePA := kmem.AllocKernelFrame()
	if queuePagePA == 0 {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate queue DMA page")
		return false
	}
	queuePageVA := queuePagePA + constants.KernelMMIOOffset

	queueSize := uint16(128) // 128 entries: desc(2048)+avail(262)+used(1030) = 3340 bytes < 4096
	endOff := virtio.VirtqueueInitOnDMAPage(&virtioBlockDevice.Queue, queueSize, queuePagePA, queuePageVA, 0)
	if endOff == 0 {
		console.KPrintln("[VirtIO Block] ERROR: Failed to init I/O queue on DMA page")
		return false
	}

	// Setup queue in device
	if !virtioPCISetupQueue(0, &virtioBlockDevice.Queue) {
		console.KPrintln("[VirtIO Block] ERROR: Failed to setup queue")
		return false
	}

	// Driver OK
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK | VIRTIO_STATUS_DRIVER_OK)

	// Check if FAILED bit is set
	finalStatus := virtioPCIGetDeviceStatus()
	if (finalStatus & VIRTIO_STATUS_FAILED) != 0 {
		console.KPrintln("[VirtIO Block] ERROR: Device failed")
		return false
	}

	return true
}

// virtioBlockReadConfig reads the device configuration
//
//go:nosplit
func virtioBlockReadConfig() bool {
	// Read capacity (8 bytes at offset 0 in device config)
	dev := &virtioBlockDevice
	deviceCfgBase := pci.ReadBAR64(dev.Bus, dev.Slot, dev.Func, dev.DeviceConfig.Bar)
	if deviceCfgBase == 0 || deviceCfgBase >= 0x100000000 {
		console.KPrintln("[VirtIO Block] ERROR: Invalid device config BAR")
		return false
	}

	// Map device config BAR if it's different from common config BAR
	commonCfgBase := pci.ReadBAR64(dev.Bus, dev.Slot, dev.Func, dev.CommonConfig.Bar)
	if deviceCfgBase != commonCfgBase {
		kmem.MapDeviceMMIO(deviceCfgBase, 0x10000)
	}

	deviceCfgAddr := deviceCfgBase + constants.KernelMMIOOffset + uintptr(dev.DeviceConfig.OffsetInBar)

	// Read capacity (64-bit value)
	capacityLow := *(*uint32)(unsafe.Pointer(deviceCfgAddr + 0))
	capacityHigh := *(*uint32)(unsafe.Pointer(deviceCfgAddr + 4))
	dev.Capacity = uint64(capacityLow) | (uint64(capacityHigh) << 32)

	// Read block size (32-bit value at offset 20)
	dev.BlockSizeBytes = *(*uint32)(unsafe.Pointer(deviceCfgAddr + 20))
	if dev.BlockSizeBytes == 0 {
		dev.BlockSizeBytes = 512 // Default to 512 if not specified
	}

	return true
}

// virtioPCISetDeviceStatus sets the device status register
//
//go:nosplit
func virtioPCISetDeviceStatus(status uint8) {
	addr := virtioBlockDevice.CommonConfigBase + VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS
	*(*uint8)(unsafe.Pointer(addr)) = status
}

// virtioPCIGetDeviceStatus reads the device status register
//
//go:nosplit
func virtioPCIGetDeviceStatus() uint8 {
	addr := virtioBlockDevice.CommonConfigBase + VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS
	return *(*uint8)(unsafe.Pointer(addr))
}

// virtioPCIWriteCommonConfig32 writes a 32-bit value to the common config space
//
//go:nosplit
func virtioPCIWriteCommonConfig32(offset uint32, value uint32) {
	addr := virtioBlockDevice.CommonConfigBase + uintptr(offset)
	*(*uint32)(unsafe.Pointer(addr)) = value
}

// virtioPCIReadCommonConfig32 reads a 32-bit value from the common config space
//
//go:nosplit
func virtioPCIReadCommonConfig32(offset uint32) uint32 {
	addr := virtioBlockDevice.CommonConfigBase + uintptr(offset)
	return *(*uint32)(unsafe.Pointer(addr))
}

// virtioPCISetupQueue sets up a virtqueue in the device
//
//go:nosplit
func virtioPCISetupQueue(queueIndex uint16, vq *virtio.VirtQueue) bool {
	base := virtioBlockDevice.CommonConfigBase

	// Select queue
	*(*uint16)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT)) = queueIndex

	// Read device's max queue size, then write our actual size
	maxQueueSize := *(*uint16)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE))
	if maxQueueSize == 0 || maxQueueSize < vq.QueueSize {
		console.KPrintf("[VirtIO Block] ERROR: Queue %d size too small (%d < %d)\n",
			queueIndex, maxQueueSize, vq.QueueSize)
		return false
	}
	// Write our chosen queue size (may be smaller than device max)
	*(*uint16)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE)) = vq.QueueSize

	// Write queue addresses (split into low/high 32-bit words)
	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW)) = uint32(vq.DescPA)
	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH)) = uint32(vq.DescPA >> 32)

	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DRIVER_LOW)) = uint32(vq.AvailPA)
	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DRIVER_HIGH)) = uint32(vq.AvailPA >> 32)

	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DEVICE_LOW)) = uint32(vq.UsedPA)
	*(*uint32)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_DEVICE_HIGH)) = uint32(vq.UsedPA >> 32)

	// Read queue_notify_off for this queue
	queueNotifyOff := *(*uint16)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_NOTIFY_OFF))
	if queueIndex == 0 {
		virtioBlockDevice.QueueNotifyOff = queueNotifyOff
	}

	// Enable queue
	*(*uint16)(unsafe.Pointer(base + VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE)) = 1

	return true
}

// Implement BlockDevice interface methods

// Name returns the device name
func (d *VirtIOBlockDevice) Name() string {
	return "virtio-block"
}

// Close shuts down the device
func (d *VirtIOBlockDevice) Close() error {
	// TODO: Implement graceful shutdown
	return nil
}

// BlockSize returns the size of a block in bytes
func (d *VirtIOBlockDevice) BlockSize() uint64 {
	return uint64(d.BlockSizeBytes)
}

// NumBlocks returns the total number of blocks
func (d *VirtIOBlockDevice) NumBlocks() uint64 {
	// Capacity is in 512-byte sectors, convert to blocks
	return d.Capacity / (uint64(d.BlockSizeBytes) / 512)
}
