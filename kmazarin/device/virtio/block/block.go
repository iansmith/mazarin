package block

import (
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/device/virtio/irq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
	"unsafe"
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

	// PCI transport: Engine-based I/O (Phase 1)
	Eng      virtio.Engine      // Owns the PCI virtqueue, submit/complete, IOComplete flag
	Sidecars virtio.SidecarPool // Device-nGnRnE slots for protocol headers (req + status)

	// MMIO transport: legacy I/O (RISC-V fallback, not Engine-managed)
	Queue          virtio.VirtQueue // Virtqueue for MMIO mode only
	QueueNotifyOff uint16           // Notify offset for MMIO mode only

	// Device configuration
	Capacity       uint64 // Total sectors
	BlockSizeBytes uint32 // Bytes per sector (typically 512)

	// MMIO transport (RISC-V): non-zero means MMIO mode, zero means PCI mode
	MMIOBase uintptr

	// DMA data buffer page.
	// PCI mode: data buffer only (header/status live in sidecar slots).
	// MMIO mode: combined header+status+data buffer (legacy layout).
	DmaPagePA uintptr // Physical address of the DMA page
	DmaPageVA uintptr // Virtual address of the DMA page (PA + KernelVAOffset)

	// Interrupt-driven I/O
	IRQNum     uint32 // Assigned IRQ number (0 = polling mode, no interrupts)
	IOComplete uint32 // Atomic: MMIO mode only (PCI uses Eng.IOComplete)

	// Phase 2: per-slot in-flight sidecar tracking (PCI only).
	// Each slot corresponds to a data buffer region at DmaPagePA + slotIdx*BlockSizeBytes.
	inFlightSidecars [MaxInFlight]virtio.SidecarSlot

	// SetAsyncMode is a callback to temporarily disable the IRQ top-half's
	// async completion mode. doBlockIO (kernel polling path) must disable
	// async mode so the top-half doesn't drain the used ring before the
	// polling loop sees it. Set by the kernel during init.
	// Returns the previous mode value.
	SetAsyncMode func(mode uint32) uint32
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

		// For PCI transport, configure MSI-X interrupts. The arch split (GICv2m
		// doorbell on arm64, LAPIC on amd64) lives inside irq.ConfigureMSIXForDevice;
		// no per-device wrapper is needed. The local is named irqNum to avoid
		// shadowing the imported `irq` package.
		if dev.MMIOBase == 0 {
			irqNum := irq.ConfigureMSIXForDevice(dev.Bus, dev.Slot, dev.Func)
			if irqNum != 0 {
				dev.IRQNum = irqNum
			}
		}

		// VirtIO handshake (MSI-X vector assigned if platform supports it)
		if !virtioBlockInit() {
			klog.Errf("[VirtIO Block] PCI device initialization failed\n")
			return false
		}
		if !virtioBlockReadConfig() {
			klog.Errf("[VirtIO Block] PCI failed to read config\n")
			return false
		}
	} else {
		// PCI not found — try MMIO (RISC-V)
		if !findVirtIOBlockMMIO() {
			klog.Errf("[VirtIO Block] No block device found (PCI or MMIO)\n")
			return false
		}
		if !virtioBlockInitMMIO() {
			klog.Errf("[VirtIO Block] MMIO device initialization failed\n")
			return false
		}
		if !virtioBlockReadConfigMMIO() {
			klog.Errf("[VirtIO Block] MMIO failed to read config\n")
			return false
		}
	}

	// Allocate a DMA-safe page for I/O buffers.
	// This avoids demand paging issues — the page has a known PA via the linear map.
	dmaPA := kmem.AllocKernelFrame()
	if dmaPA == 0 {
		klog.Errf("[VirtIO Block] ERROR: Failed to allocate DMA page\n")
		return false
	}
	virtioBlockDevice.DmaPagePA = dmaPA
	virtioBlockDevice.DmaPageVA = dmaPA + constants.KernelVAOffset

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
						klog.Errf("[VirtIO Block] ERROR: Failed to find/map BARs\n")
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
// Sets up the Engine for PCI-based submit/complete and the SidecarPool for
// protocol headers (VirtIOBlockReq + status byte).
// Returns true on success, false on failure.
//
//go:nosplit
func virtioBlockInit() bool {
	dev := &virtioBlockDevice

	// Feature negotiation: VIRTIO_F_VERSION_1 + RING_EVENT_IDX (MAZ-136).
	// EVENT_IDX lets us coalesce block completion interrupts to one per batch via
	// used_event (VirtqueueSetUsedEvent) instead of ~1 IRQ per 4 KB block — decisive
	// on x86 nested-KVM where each interrupt injection costs ~ms.
	if !dev.Handshake(virtio.VIRTIO_F_RING_EVENT_IDX, virtio.FeatureVersion1) {
		klog.Errf("[VirtIO Block] ERROR: Device rejected features\n")
		return false
	}

	// MSI-X config vector — shared with net via irq.DeviceMSIXVector (= 0). Block
	// is MSI-X on both arches; the arch-specific MSI-X programming has already
	// happened in irq.ConfigureMSIXForDevice. (The stale "INTx on ARM64/RISC-V"
	// comment was leftover from when RISC-V used INTx; RISC-V was removed in
	// a04ce0a and ARM64 moved to MSI-X.)
	msixVec := irq.DeviceMSIXVector
	dev.SetMSIXConfig(msixVec)

	// Allocate DMA page for virtqueue structures
	queuePagePA := kmem.AllocKernelFrame()
	if queuePagePA == 0 {
		klog.Errf("[VirtIO Block] ERROR: Failed to allocate queue DMA page\n")
		return false
	}
	queuePageVA := queuePagePA + constants.KernelVAOffset

	// Initialize Engine: sets up VQ on DMA page, enables queue on device.
	// Queue 0, 128 entries: desc(2048)+avail(262)+used(1030) = 3340 bytes < 4096
	if !dev.Eng.Init(&dev.PCIDevice, 0, 128, queuePagePA, queuePageVA, 0, msixVec) {
		klog.Errf("[VirtIO Block] ERROR: Failed to init engine\n")
		return false
	}

	// Initialize sidecar pool for protocol headers (Device-nGnRnE page).
	// Each 32-byte slot holds: VirtIOBlockReq (16B) + status (1B) + padding.
	if !dev.Sidecars.Init(sidecarSlotSize) {
		klog.Errf("[VirtIO Block] ERROR: Failed to init sidecar pool\n")
		return false
	}

	// Complete handshake
	dev.SetDriverOK()

	if dev.CheckFailed() {
		klog.Errf("[VirtIO Block] ERROR: Device failed\n")
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
		klog.Errf("[VirtIO Block] ERROR: No device config BAR mapped\n")
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
// PCI mode uses Engine.IOComplete (set by NonTimerIRQTopHalf).
// MMIO mode uses device-level IOComplete (unused — MMIO has no IRQ).
func GetIOCompletePtr() *uint32 {
	return &virtioBlockDevice.Eng.IOComplete
}

// GetEnginePtr returns a uintptr to the Engine (for storing in bottom_half
// without importing this package in nosplit top-half code).
func GetEnginePtr() uintptr {
	return uintptr(unsafe.Pointer(&virtioBlockDevice.Eng))
}

// GetInFlightSidecar returns the sidecar slot for the given in-flight index.
func (d *VirtIOBlockDevice) GetInFlightSidecar(slotIdx uint8) virtio.SidecarSlot {
	return d.inFlightSidecars[slotIdx]
}

// GetSidecarFreeBitsPtr returns a pointer to the sidecar pool's FreeBits
// field. The top-half uses this to release sidecar slots after async completion.
func GetSidecarFreeBitsPtr() *uint64 {
	return &virtioBlockDevice.Sidecars.FreeBits
}

// GetDevice returns a pointer to the global block device instance.
func GetDevice() *VirtIOBlockDevice {
	return &virtioBlockDevice
}


