package block

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
)

// VirtIO block PCI device IDs
const (
	VIRTIO_BLK_DEVICE_ID_LEGACY = 0x1001 // Transitional device
	VIRTIO_BLK_DEVICE_ID_MODERN = 0x1042 // Non-transitional (VirtIO 1.0+)
)

// VirtIOBlockDevice represents a VirtIO block device.
// Embeds virtio.PCIDevice for shared PCI transport state (Bus/Slot/Func,
// capability info, CommonConfigBase, NotifyBase, ISRBase, DeviceConfigBase).
type VirtIOBlockDevice struct {
	virtio.PCIDevice // Shared PCI transport (promoted fields: Bus, Slot, Func, etc.)

	// VirtQueue for I/O
	Queue          virtio.VirtQueue
	QueueNotifyOff uint16 // Notify offset for I/O queue

	// Device configuration
	Capacity       uint64 // Total sectors
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

	// Interrupt-driven I/O (set by ConfigureInterrupts after SetVBAR)
	IRQNum     uint32 // Assigned IRQ number (0 = polling mode, no interrupts)
	IOComplete uint32 // Atomic: set to 1 by IRQ top-half when I/O completes
}

// Global device instance
var virtioBlockDevice VirtIOBlockDevice

// Init initializes the VirtIO block device.
// Tries PCI first (works on all architectures), falls back to MMIO (RISC-V).
// PCI transport uses MSI-X on AMD64 and ARM64 (via GICv2m), INTx on RISC-V (via PLIC).
// The caller must enable the interrupt after Init returns.
// Returns true on success, false if no device found or initialization failed.
func Init() bool {
	// Try PCI first (all architectures)
	if findVirtIOBlock() {
		dev := &virtioBlockDevice

		// For PCI transport, configure interrupts (MSI-X on AMD64/ARM64, INTx on RISC-V).
		if dev.MMIOBase == 0 {
			irq := configureBlockInterrupt(dev.Bus, dev.Slot, dev.Func)
			if irq != 0 {
				dev.IRQNum = irq
			}
		}

		// VirtIO handshake (MSI-X vector assigned if platform supports it)
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

	if virtioBlockDevice.IRQNum != 0 {
		console.KPrintf("[VirtIO Block] Initialized: %d MB (%d sectors) IRQ=%d\n",
			virtioBlockDevice.Capacity/2048, virtioBlockDevice.Capacity, virtioBlockDevice.IRQNum)
	} else {
		console.KPrintf("[VirtIO Block] Initialized: %d MB (%d sectors) polling\n",
			virtioBlockDevice.Capacity/2048, virtioBlockDevice.Capacity)
	}

	// Register with device manager
	device.RegisterBlockDevice(&virtioBlockDevice)

	return true
}

// findVirtIOBlock scans the PCI bus for a VirtIO block device.
// On success, populates the embedded PCIDevice with BAR mappings.
func findVirtIOBlock() bool {
	// Use PCI_MMIO_BASE + 0x200000 to avoid collision with GPU BARs
	const blockMMIOBase = pci.PCI_MMIO_BASE + 0x200000

	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			fullReg0 := pci.ConfigRead32(bus, slot, 0, pci.PCI_VENDOR_ID)
			vendorID0 := fullReg0 & 0xFFFF
			if vendorID0 == 0xFFFF || vendorID0 == 0 {
				continue
			}

			headerType := pci.ConfigRead32(bus, slot, 0, 0x0C)
			isMultiFunc := (headerType>>16)&0x80 != 0
			maxFunc := uint8(1)
			if isMultiFunc {
				maxFunc = 8
			}

			for funcNum := uint8(0); funcNum < maxFunc; funcNum++ {
				var fullReg uint32
				if funcNum == 0 {
					fullReg = fullReg0
				} else {
					fullReg = pci.ConfigRead32(bus, slot, funcNum, pci.PCI_VENDOR_ID)
				}
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				if vendorID == pci.VIRTIO_VENDOR_ID &&
					(deviceID == VIRTIO_BLK_DEVICE_ID_LEGACY || deviceID == VIRTIO_BLK_DEVICE_ID_MODERN) {

					if !virtioBlockDevice.FindAndMapBARs(bus, slot, funcNum, blockMMIOBase) {
						console.KPrintln("[VirtIO Block] ERROR: Failed to find/map BARs")
						return false
					}
					return true
				}
			}
		}
	}

	return false
}

// virtioBlockInit initializes the VirtIO block device via the VirtIO handshake.
// On AMD64 and ARM64, uses MSI-X (configured by configureBlockInterrupt before
// this function is called). On RISC-V, uses INTx (PCI legacy interrupts via PLIC).
// Returns true on success, false on failure.
//
//go:nosplit
func virtioBlockInit() bool {
	dev := &virtioBlockDevice

	// Feature negotiation: accept VIRTIO_F_VERSION_1 only
	if !dev.Handshake(0, virtio.FeatureVersion1) {
		console.KPrintln("[VirtIO Block] ERROR: Device rejected features")
		return false
	}

	// MSI-X config vector. On INTx platforms (ARM64/RISC-V), blockMSIXVector()
	// returns MSIXNoVector and SetMSIXConfig skips the write.
	msixVec := blockMSIXVector()
	dev.SetMSIXConfig(msixVec)

	// Allocate DMA page for virtqueue structures
	queuePagePA := kmem.AllocKernelFrame()
	if queuePagePA == 0 {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate queue DMA page")
		return false
	}
	queuePageVA := queuePagePA + constants.KernelMMIOOffset

	// Set up queue 0: 128 entries, desc(2048)+avail(262)+used(1030) = 3340 bytes < 4096
	notifyOff, ok := dev.SetupQueue(0, &dev.Queue, 128, queuePagePA, queuePageVA, 0, msixVec)
	if !ok {
		console.KPrintln("[VirtIO Block] ERROR: Failed to setup queue")
		return false
	}
	dev.QueueNotifyOff = notifyOff

	// Complete handshake
	dev.SetDriverOK()

	if dev.CheckFailed() {
		console.KPrintln("[VirtIO Block] ERROR: Device failed")
		return false
	}

	return true
}

// virtioBlockReadConfig reads the device configuration.
// DeviceConfigBase was set by FindAndMapBARs during PCI discovery.
//
//go:nosplit
func virtioBlockReadConfig() bool {
	dev := &virtioBlockDevice
	if dev.DeviceConfigBase == 0 {
		console.KPrintln("[VirtIO Block] ERROR: No device config BAR mapped")
		return false
	}

	// Read capacity (64-bit value at offset 0)
	dev.Capacity = dev.ReadDeviceConfig64(0)

	// Read block size (32-bit value at offset 20)
	dev.BlockSizeBytes = dev.ReadDeviceConfig32(20)
	if dev.BlockSizeBytes == 0 {
		dev.BlockSizeBytes = 512 // Default to 512 if not specified
	}

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

// ConfigureInterrupts is kept for backward compatibility but is now a no-op.
// INTx interrupt routing is configured during Init(). The caller should use
// GetIRQNum/GetISRBase/GetIOCompletePtr to wire up the top-half dispatcher.
func ConfigureInterrupts() {
}

// GetIRQNum returns the assigned IRQ number (0 = polling mode).
func GetIRQNum() uint32 {
	return virtioBlockDevice.IRQNum
}

// GetISRBase returns the ISR register VA for interrupt acknowledgement.
func GetISRBase() uintptr {
	return virtioBlockDevice.ISRBase
}

// GetIOCompletePtr returns a pointer to the IOComplete atomic flag.
func GetIOCompletePtr() *uint32 {
	return &virtioBlockDevice.IOComplete
}

// GetDevice returns a pointer to the global block device instance.
func GetDevice() *VirtIOBlockDevice {
	return &virtioBlockDevice
}


