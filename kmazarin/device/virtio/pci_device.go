// pci_device.go — Shared VirtIO PCI transport state and operations.
//
// PCIDevice consolidates the duplicated PCI transport logic from block, gpu,
// input, and rng packages: BAR discovery, feature negotiation handshake,
// virtqueue setup, and device notification.
//
// Each driver embeds or holds a pointer to PCIDevice and delegates the common
// PCI transport operations to these methods, keeping only device-specific
// logic (e.g., block request types, GPU command encoding) in their own package.
//
// Per-arch interrupt configuration is in pci_device_{arm64,amd64,riscv64}.go.
//
// Reference: VirtIO Specification 1.2, sections 2 and 4.1.

package virtio

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
	"unsafe"
)

// PCIDevice holds the shared PCI transport state for any VirtIO device.
// Drivers embed this (or hold a pointer to it) alongside device-specific state.
type PCIDevice struct {
	// PCI location
	Bus, Slot, Func uint8

	// VirtIO PCI capability info (from FindVirtIOCapabilities)
	CommonConfig pci.VirtIOCapabilityInfo
	NotifyConfig pci.VirtIOCapabilityInfo
	ISRConfig    pci.VirtIOCapabilityInfo
	DeviceConfig pci.VirtIOCapabilityInfo

	// Mapped virtual addresses (PA + KernelMMIOOffset + OffsetInBar)
	CommonConfigBase uintptr // Common config register base VA
	NotifyBase       uintptr // Notify register base VA
	ISRBase          uintptr // ISR status register VA (for interrupt acknowledgement)
	DeviceConfigBase uintptr // Device-specific config register base VA

	// Interrupt state
	LogicalIRQ  uint32 // Platform-independent (e.g., BlockVirtualIRQ=201)
	HardwareIRQ uint32 // Platform-specific (GIC SPI, LAPIC vector, PLIC source)
}

// FindAndMapBARs discovers VirtIO capabilities and maps the relevant BARs.
// mmioBase is the PCI MMIO base address to reprogram BARs to if they are
// zero or above 4GB (e.g., pci.PCI_MMIO_BASE + 0x200000 for block).
//
// Populates the capability info fields and CommonConfigBase, NotifyBase,
// ISRBase, and DeviceConfigBase. Returns true on success.
func (d *PCIDevice) FindAndMapBARs(bus, slot, funcNum uint8, mmioBase uintptr) bool {
	d.Bus = bus
	d.Slot = slot
	d.Func = funcNum

	// Enable device: I/O, memory, bus master
	cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
	cmd |= 0x7
	pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

	// Find VirtIO capabilities
	if !pci.FindVirtIOCapabilities(bus, slot, funcNum,
		&d.CommonConfig, &d.NotifyConfig, &d.ISRConfig, &d.DeviceConfig) {
		return false
	}

	// Map common config BAR
	commonBarBase := pci.ReadBAR64(bus, slot, funcNum, d.CommonConfig.Bar)
	if commonBarBase == 0 || commonBarBase >= 0x100000000 {
		pci.WriteBAR64(bus, slot, funcNum, d.CommonConfig.Bar, mmioBase)
		commonBarBase = pci.ReadBAR64(bus, slot, funcNum, d.CommonConfig.Bar)
	}
	kmem.MapDeviceMMIO(commonBarBase, 0x10000)
	d.CommonConfigBase = commonBarBase + constants.KernelMMIOOffset + uintptr(d.CommonConfig.OffsetInBar)

	// Map notify BAR (may be same as common)
	notifyBarBase := pci.ReadBAR64(bus, slot, funcNum, d.NotifyConfig.Bar)
	if notifyBarBase == 0 || notifyBarBase >= 0x100000000 {
		pci.WriteBAR64(bus, slot, funcNum, d.NotifyConfig.Bar, mmioBase+0x10000)
		notifyBarBase = pci.ReadBAR64(bus, slot, funcNum, d.NotifyConfig.Bar)
	}
	if notifyBarBase != commonBarBase {
		kmem.MapDeviceMMIO(notifyBarBase, 0x10000)
	}
	d.NotifyBase = notifyBarBase + constants.KernelMMIOOffset + uintptr(d.NotifyConfig.OffsetInBar)

	// Map ISR BAR (optional, may be same as common)
	if d.ISRConfig.Offset != 0 {
		isrBarBase := pci.ReadBAR64(bus, slot, funcNum, d.ISRConfig.Bar)
		if isrBarBase != 0 && isrBarBase < 0x100000000 {
			if isrBarBase != commonBarBase && isrBarBase != notifyBarBase {
				kmem.MapDeviceMMIO(isrBarBase, 0x10000)
			}
			d.ISRBase = isrBarBase + constants.KernelMMIOOffset + uintptr(d.ISRConfig.OffsetInBar)
		}
	}

	// Map device config BAR (optional, may be same as common)
	if d.DeviceConfig.Offset != 0 {
		devCfgBarBase := pci.ReadBAR64(bus, slot, funcNum, d.DeviceConfig.Bar)
		if devCfgBarBase != 0 && devCfgBarBase < 0x100000000 {
			if devCfgBarBase != commonBarBase && devCfgBarBase != notifyBarBase {
				kmem.MapDeviceMMIO(devCfgBarBase, 0x10000)
			}
			d.DeviceConfigBase = devCfgBarBase + constants.KernelMMIOOffset + uintptr(d.DeviceConfig.OffsetInBar)
		}
	}

	return true
}

// Handshake performs the VirtIO feature negotiation handshake (spec sections 3.1.1).
// Steps: Reset → ACKNOWLEDGE → DRIVER → negotiate features → FEATURES_OK → verify.
//
// acceptedFeaturesLow/High are the feature bits the driver wants to accept.
// Typically acceptedFeaturesLow=0, acceptedFeaturesHigh=FeatureVersion1 (VIRTIO_F_VERSION_1).
//
// Returns true if the device accepted the features. Does NOT set DRIVER_OK — the
// caller must set up queues and call SetDriverOK() after this returns.
//
//go:nosplit
func (d *PCIDevice) Handshake(acceptedFeaturesLow, acceptedFeaturesHigh uint32) bool {
	// Step 1: Reset device
	d.SetDeviceStatus(0)

	// Step 2-3: Acknowledge and indicate driver present
	d.SetDeviceStatus(StatusAcknowledge)
	d.SetDeviceStatus(StatusAcknowledge | StatusDriver)

	// Step 4: Feature negotiation — read device features (both pages)
	d.WriteCommonConfig32(CfgDeviceFeatureSelect, 0)
	d.ReadCommonConfig32(CfgDeviceFeature) // low 32 bits (read and discard)
	d.WriteCommonConfig32(CfgDeviceFeatureSelect, 1)
	d.ReadCommonConfig32(CfgDeviceFeature) // high 32 bits (read and discard)

	// Write accepted features
	d.WriteCommonConfig32(CfgDriverFeatureSelect, 0)
	d.WriteCommonConfig32(CfgDriverFeature, acceptedFeaturesLow)
	d.WriteCommonConfig32(CfgDriverFeatureSelect, 1)
	d.WriteCommonConfig32(CfgDriverFeature, acceptedFeaturesHigh)

	// Step 5: FEATURES_OK
	d.SetDeviceStatus(StatusAcknowledge | StatusDriver | StatusFeaturesOK)

	// Verify FEATURES_OK is still set (device may reject our feature selection)
	status := d.GetDeviceStatus()
	if (status & StatusFeaturesOK) == 0 {
		return false
	}

	return true
}

// EnableQueue writes an already-initialized VirtQueue's addresses to the device
// and enables it. Works with both heap-allocated queues (VirtqueueInit, where
// PAs are computed via VirtqueueGetPhysicalAddr) and DMA-page queues
// (VirtqueueInitOnDMAPage, where PAs are stored in DescPA/AvailPA/UsedPA).
//
// Returns the queue's notify offset and true on success, or (0, false) on failure.
//
//go:nosplit
func (d *PCIDevice) EnableQueue(qIdx uint16, vq *VirtQueue, msixVec uint16) (notifyOff uint16, ok bool) {
	base := d.CommonConfigBase

	// Select queue
	asm.MmioWrite16(base+CfgQueueSelect, qIdx)

	// Read device's max queue size
	maxQueueSize := asm.MmioRead16(base + CfgQueueSize)
	if maxQueueSize == 0 || maxQueueSize < vq.QueueSize {
		return 0, false
	}

	// Write our queue size
	asm.MmioWrite16(base+CfgQueueSize, vq.QueueSize)

	// Get physical addresses. DMA-page queues have DescPA set;
	// heap-allocated queues have DescPA=0 and need VA→PA conversion.
	descPA := vq.DescPA
	availPA := vq.AvailPA
	usedPA := vq.UsedPA
	if descPA == 0 {
		descPA = VirtqueueGetPhysicalAddr(vq.DescTable)
		availPA = VirtqueueGetPhysicalAddr(unsafe.Pointer(vq.Available))
		usedPA = VirtqueueGetPhysicalAddr(unsafe.Pointer(vq.Used))
	}

	// Write queue addresses
	asm.MmioWrite32(base+CfgQueueDescLow, uint32(descPA))
	asm.MmioWrite32(base+CfgQueueDescHigh, uint32(descPA>>32))
	asm.MmioWrite32(base+CfgQueueAvailLow, uint32(availPA))
	asm.MmioWrite32(base+CfgQueueAvailHigh, uint32(availPA>>32))
	asm.MmioWrite32(base+CfgQueueUsedLow, uint32(usedPA))
	asm.MmioWrite32(base+CfgQueueUsedHigh, uint32(usedPA>>32))

	// Read queue_notify_off for this queue
	notifyOff = asm.MmioRead16(base + CfgQueueNotifyOff)

	// Assign MSI-X vector to this queue (skip if MSIXNoVector)
	if msixVec != MSIXNoVector {
		asm.MmioWrite16(base+CfgQueueMSIXVector, msixVec)
	}

	// Enable queue
	asm.MmioWrite16(base+CfgQueueEnable, 1)

	return notifyOff, true
}

// SetupQueue initializes a virtqueue on a pre-allocated DMA page, writes its
// addresses to the device, and enables it. Convenience wrapper around
// VirtqueueInitOnDMAPage + EnableQueue.
//
// Returns the queue's notify offset and true on success, or (0, false) on failure.
//
//go:nosplit
func (d *PCIDevice) SetupQueue(qIdx uint16, vq *VirtQueue, queueSize uint16,
	queuePagePA, queuePageVA uintptr, pageOffset uintptr, msixVec uint16) (notifyOff uint16, ok bool) {

	endOff := VirtqueueInitOnDMAPage(vq, queueSize, queuePagePA, queuePageVA, pageOffset)
	if endOff == 0 {
		return 0, false
	}

	return d.EnableQueue(qIdx, vq, msixVec)
}

// SetMSIXConfig writes the MSI-X config vector to the device's common config.
// Pass MSIXNoVector to skip MSI-X configuration (e.g., on INTx platforms).
//
//go:nosplit
func (d *PCIDevice) SetMSIXConfig(msixVec uint16) {
	if msixVec != MSIXNoVector {
		asm.MmioWrite16(d.CommonConfigBase+CfgMSIXConfig, msixVec)
	}
}

// SetDriverOK sets the DRIVER_OK status bit, completing the handshake.
// Call this after all queues are set up.
//
//go:nosplit
func (d *PCIDevice) SetDriverOK() {
	d.SetDeviceStatus(StatusAcknowledge | StatusDriver | StatusFeaturesOK | StatusDriverOK)
}

// CheckFailed returns true if the device has set the FAILED status bit.
//
//go:nosplit
func (d *PCIDevice) CheckFailed() bool {
	return (d.GetDeviceStatus() & StatusFailed) != 0
}

// Notify sends a virtqueue notification to the device via the PCI notify register.
// queueNotifyOff is the per-queue notify offset (from SetupQueue return value).
// queueIndex is the queue index being notified.
//
//go:nosplit
func (d *PCIDevice) Notify(queueNotifyOff, queueIndex uint16) {
	notifyAddr := d.NotifyBase +
		uintptr(queueNotifyOff)*uintptr(d.NotifyConfig.NotifyOffMultiplier)
	asm.MmioWrite16(notifyAddr, queueIndex)
	asm.Dsb()
}

// --- Low-level common config accessors ---

// SetDeviceStatus writes the 8-bit device status register.
//
//go:nosplit
func (d *PCIDevice) SetDeviceStatus(status uint8) {
	asm.MmioWrite8(d.CommonConfigBase+CfgDeviceStatus, status)
}

// GetDeviceStatus reads the 8-bit device status register.
//
//go:nosplit
func (d *PCIDevice) GetDeviceStatus() uint8 {
	return asm.MmioRead8(d.CommonConfigBase + CfgDeviceStatus)
}

// WriteCommonConfig32 writes a 32-bit value to a common config register.
//
//go:nosplit
func (d *PCIDevice) WriteCommonConfig32(offset uintptr, value uint32) {
	asm.MmioWrite32(d.CommonConfigBase+offset, value)
}

// ReadCommonConfig32 reads a 32-bit value from a common config register.
//
//go:nosplit
func (d *PCIDevice) ReadCommonConfig32(offset uintptr) uint32 {
	return asm.MmioRead32(d.CommonConfigBase + offset)
}

// WriteCommonConfig16 writes a 16-bit value to a common config register.
//
//go:nosplit
func (d *PCIDevice) WriteCommonConfig16(offset uintptr, value uint16) {
	asm.MmioWrite16(d.CommonConfigBase+offset, value)
}

// ReadCommonConfig16 reads a 16-bit value from a common config register.
//
//go:nosplit
func (d *PCIDevice) ReadCommonConfig16(offset uintptr) uint16 {
	return asm.MmioRead16(d.CommonConfigBase + offset)
}

// ReadDeviceConfig32 reads a 32-bit value from the device-specific config space.
// offset is relative to the start of the device config region.
//
//go:nosplit
func (d *PCIDevice) ReadDeviceConfig32(offset uintptr) uint32 {
	return asm.MmioRead32(d.DeviceConfigBase + offset)
}

// ReadDeviceConfig64 reads a 64-bit value from the device-specific config space
// as two 32-bit reads (low then high). offset is relative to the device config region.
//
//go:nosplit
func (d *PCIDevice) ReadDeviceConfig64(offset uintptr) uint64 {
	low := asm.MmioRead32(d.DeviceConfigBase + offset)
	high := asm.MmioRead32(d.DeviceConfigBase + offset + 4)
	return uint64(low) | (uint64(high) << 32)
}

// ReadISRStatus reads and clears the ISR status register.
// Used by INTx interrupt handlers to acknowledge the interrupt.
//
//go:nosplit
func (d *PCIDevice) ReadISRStatus() uint8 {
	if d.ISRBase == 0 {
		return 0
	}
	return asm.MmioRead8(d.ISRBase)
}

// LogDevice is a no-op retained for API compatibility.
func (d *PCIDevice) LogDevice(tag string) {
}

