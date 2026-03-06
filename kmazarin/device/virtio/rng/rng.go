// Package rng implements a VirtIO RNG (entropy) device driver using PCI transport.
//
// VirtIO RNG is the simplest VirtIO device: it has a single virtqueue (requestq).
// The driver submits device-writable buffers; the device fills them with random
// bytes and returns them on the used ring.
//
// Spec reference: VirtIO 1.0+ §5.4 (Entropy Device)
package rng

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
	"unsafe"
)

// VirtIO PCI device IDs for RNG
const (
	VIRTIO_RNG_DEVICE_ID_MODERN       = 0x1044 // Non-transitional (VirtIO 1.0+)
	VIRTIO_RNG_DEVICE_ID_TRANSITIONAL = 0x1005 // Transitional (legacy compatible)
)

// VirtIO status bits (same as block, redefined to avoid cross-package dep)
const (
	statusAcknowledge = 1
	statusDriver      = 2
	statusDriverOk    = 4
	statusFeaturesOk  = 8
)

// VirtIO PCI common config offsets (same as block)
const (
	cfgDeviceFeatureSelect = 0x00
	cfgDeviceFeature       = 0x04
	cfgDriverFeatureSelect = 0x08
	cfgDriverFeature       = 0x0C
	cfgDeviceStatus        = 0x14
	cfgQueueSelect         = 0x16
	cfgQueueSize           = 0x18
	cfgQueueEnable         = 0x1C
	cfgQueueNotifyOff      = 0x1E
	cfgQueueDescLow        = 0x20
	cfgQueueDescHigh       = 0x24
	cfgQueueDriverLow      = 0x28
	cfgQueueDriverHigh     = 0x2C
	cfgQueueDeviceLow      = 0x30
	cfgQueueDeviceHigh     = 0x34
)

// rngDevice holds the state for the VirtIO RNG PCI device.
type rngDevice struct {
	Bus, Slot, Func uint8

	CommonConfig pci.VirtIOCapabilityInfo
	NotifyConfig pci.VirtIOCapabilityInfo
	ISRConfig    pci.VirtIOCapabilityInfo

	CommonConfigBase uintptr
	NotifyBase       uintptr
	ISRBase          uintptr

	Queue          virtio.VirtQueue
	QueueNotifyOff uint16

	// DMA page layout:
	//   [0..63]  result buffer (up to 64 bytes of entropy per request)
	DmaPagePA uintptr
	DmaPageVA uintptr

	Found bool
}

var dev rngDevice

// Init discovers and initializes the VirtIO RNG device on the PCI bus.
// Returns true if the device was found and initialized successfully.
func Init() bool {
	if !findDevice() {
		return false
	}

	if !handshake() {
		console.KPrintln("[VirtIO RNG] PCI handshake failed")
		return false
	}

	// Allocate DMA page for result buffers
	dmaPA := kmem.AllocKernelFrame()
	if dmaPA == 0 {
		console.KPrintln("[VirtIO RNG] ERROR: Failed to allocate DMA page")
		return false
	}
	dev.DmaPagePA = dmaPA
	dev.DmaPageVA = dmaPA + constants.KernelMMIOOffset

	console.KPrintf("[VirtIO RNG] Initialized (bus=%d slot=%d func=%d)\n",
		dev.Bus, dev.Slot, dev.Func)

	return true
}

// Get fills buf with random bytes from the VirtIO RNG device using polling.
// Returns the number of bytes written.
func Get(buf []byte) int {
	if !dev.Found || len(buf) == 0 {
		return 0
	}

	n := len(buf)
	if n > 64 {
		n = 64 // limit to DMA buffer size
	}

	// Zero the DMA result buffer
	resultBuf := unsafe.Slice((*byte)(unsafe.Pointer(dev.DmaPageVA)), 64)
	for i := range resultBuf {
		resultBuf[i] = 0
	}

	// Submit a device-writable descriptor: the device fills it with entropy.
	vq := &dev.Queue
	descIdx := vq.FreeHead
	if vq.NumFree == 0 {
		return 0
	}

	descSize := unsafe.Sizeof(virtio.VirtQDesc{})
	desc := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(descIdx)*descSize))
	desc.Addr = uint64(dev.DmaPagePA) // physical address for DMA
	desc.Len = uint32(n)
	desc.Flags = virtio.VIRTQ_DESC_F_WRITE // device writes to this buffer
	desc.Next = 0

	vq.FreeHead = desc.Next
	vq.NumFree--

	// Add to available ring (Ring is [0]uint16 — use unsafe pointer arithmetic)
	avail := vq.Available
	ringEntry := (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(avail)) + 4 + uintptr(avail.Idx%vq.QueueSize)*2))
	*ringEntry = descIdx
	// Memory barrier before updating idx
	*(*uint16)(unsafe.Pointer(&avail.Idx)) = avail.Idx + 1

	// Notify the device
	notifyAddr := dev.NotifyBase + uintptr(dev.QueueNotifyOff)*2
	*(*uint16)(unsafe.Pointer(notifyAddr)) = 0

	// Poll the ISR register until the device signals completion
	for i := 0; i < 1000000; i++ {
		isr := *(*uint8)(unsafe.Pointer(dev.ISRBase))
		if isr&1 != 0 {
			break
		}
	}

	// Check used ring for completion
	used := vq.Used
	if used.Idx == vq.LastUsedIdx {
		// Timeout — return descriptor to free list
		vq.FreeHead = descIdx
		vq.NumFree++
		return 0
	}

	usedElemPtr := (*virtio.VirtQUsedElem)(unsafe.Pointer(uintptr(unsafe.Pointer(used)) + 4 + uintptr(vq.LastUsedIdx%vq.QueueSize)*unsafe.Sizeof(virtio.VirtQUsedElem{})))
	usedElem := *usedElemPtr
	bytesWritten := int(usedElem.Len)
	vq.LastUsedIdx++

	// Return descriptor to free list
	vq.FreeHead = descIdx
	vq.NumFree++

	if bytesWritten > n {
		bytesWritten = n
	}

	// Copy from DMA buffer to caller's buffer
	copy(buf[:bytesWritten], resultBuf[:bytesWritten])

	return bytesWritten
}

// findDevice scans the PCI bus for a modern VirtIO RNG device.
func findDevice() bool {
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

				if vendorID == pci.VIRTIO_VENDOR_ID && (deviceID == VIRTIO_RNG_DEVICE_ID_MODERN || deviceID == VIRTIO_RNG_DEVICE_ID_TRANSITIONAL) {
					// Enable device
					cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
					cmd |= 0x7 // I/O + memory + bus master
					pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

					// Find VirtIO capabilities
					var common, notify, isr, deviceCfg pci.VirtIOCapabilityInfo
					if !pci.FindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &deviceCfg) {
						console.KPrintln("[VirtIO RNG] ERROR: VirtIO capabilities not found")
						return false
					}

					// Map common config BAR
					barBase := pci.ReadBAR64(bus, slot, funcNum, common.Bar)
					const rngMMIOBase = pci.PCI_MMIO_BASE + 0x300000
					if barBase == 0 || barBase >= 0x100000000 {
						pci.WriteBAR64(bus, slot, funcNum, common.Bar, rngMMIOBase)
						barBase = pci.ReadBAR64(bus, slot, funcNum, common.Bar)
					}
					kmem.MapDeviceMMIO(barBase, 0x10000)

					dev.Bus = bus
					dev.Slot = slot
					dev.Func = funcNum
					dev.CommonConfig = common
					dev.NotifyConfig = notify
					dev.ISRConfig = isr
					dev.CommonConfigBase = barBase + constants.KernelMMIOOffset + uintptr(common.OffsetInBar)

					// Map ISR BAR
					if isr.Offset != 0 {
						isrBarBase := pci.ReadBAR64(bus, slot, funcNum, isr.Bar)
						if isrBarBase != 0 && isrBarBase < 0x100000000 {
							if isrBarBase != barBase {
								kmem.MapDeviceMMIO(isrBarBase, 0x10000)
							}
							dev.ISRBase = isrBarBase + constants.KernelMMIOOffset + uintptr(isr.OffsetInBar)
						}
					}

					// Map notify BAR
					notifyBarBase := pci.ReadBAR64(bus, slot, funcNum, notify.Bar)
					if notifyBarBase == 0 || notifyBarBase >= 0x100000000 {
						pci.WriteBAR64(bus, slot, funcNum, notify.Bar, rngMMIOBase+0x10000)
						notifyBarBase = pci.ReadBAR64(bus, slot, funcNum, notify.Bar)
					}
					if notifyBarBase != barBase {
						kmem.MapDeviceMMIO(notifyBarBase, 0x10000)
					}
					dev.NotifyBase = notifyBarBase + constants.KernelMMIOOffset + uintptr(notify.OffsetInBar)

					dev.Found = true
					return true
				}
			}
		}
	}

	return false
}

// handshake performs the VirtIO 1.0 initialization sequence.
func handshake() bool {
	base := dev.CommonConfigBase

	// Reset
	setStatus(0)

	// Acknowledge + Driver
	setStatus(statusAcknowledge)
	setStatus(statusAcknowledge | statusDriver)

	// Feature negotiation: accept VIRTIO_F_VERSION_1 only
	writeCommon32(cfgDeviceFeatureSelect, 0)
	readCommon32(cfgDeviceFeature)
	writeCommon32(cfgDeviceFeatureSelect, 1)
	readCommon32(cfgDeviceFeature)

	writeCommon32(cfgDriverFeatureSelect, 0)
	writeCommon32(cfgDriverFeature, 0)
	writeCommon32(cfgDriverFeatureSelect, 1)
	writeCommon32(cfgDriverFeature, 1) // VIRTIO_F_VERSION_1

	// Features OK
	setStatus(statusAcknowledge | statusDriver | statusFeaturesOk)
	if getStatus()&statusFeaturesOk == 0 {
		console.KPrintln("[VirtIO RNG] ERROR: Device rejected features")
		return false
	}

	// Allocate DMA page for virtqueue structures
	queuePagePA := kmem.AllocKernelFrame()
	if queuePagePA == 0 {
		console.KPrintln("[VirtIO RNG] ERROR: Failed to allocate queue DMA page")
		return false
	}
	queuePageVA := queuePagePA + constants.KernelMMIOOffset

	// Configure queue 0 — read max size before allocating
	*(*uint16)(unsafe.Pointer(base + cfgQueueSelect)) = 0

	maxQueueSize := *(*uint16)(unsafe.Pointer(base + cfgQueueSize))
	if maxQueueSize == 0 {
		console.KPrintln("[VirtIO RNG] ERROR: Device reports queue size 0")
		return false
	}

	// Use device's max or our preferred size, whichever is smaller
	queueSize := uint16(16)
	if maxQueueSize < queueSize {
		queueSize = maxQueueSize
	}

	endOff := virtio.VirtqueueInitOnDMAPage(&dev.Queue, queueSize, queuePagePA, queuePageVA, 0)
	if endOff == 0 {
		console.KPrintln("[VirtIO RNG] ERROR: Failed to init queue on DMA page")
		return false
	}

	*(*uint16)(unsafe.Pointer(base + cfgQueueSize)) = queueSize

	vq := &dev.Queue
	*(*uint32)(unsafe.Pointer(base + cfgQueueDescLow)) = uint32(vq.DescPA)
	*(*uint32)(unsafe.Pointer(base + cfgQueueDescHigh)) = uint32(vq.DescPA >> 32)
	*(*uint32)(unsafe.Pointer(base + cfgQueueDriverLow)) = uint32(vq.AvailPA)
	*(*uint32)(unsafe.Pointer(base + cfgQueueDriverHigh)) = uint32(vq.AvailPA >> 32)
	*(*uint32)(unsafe.Pointer(base + cfgQueueDeviceLow)) = uint32(vq.UsedPA)
	*(*uint32)(unsafe.Pointer(base + cfgQueueDeviceHigh)) = uint32(vq.UsedPA >> 32)

	dev.QueueNotifyOff = *(*uint16)(unsafe.Pointer(base + cfgQueueNotifyOff))

	*(*uint16)(unsafe.Pointer(base + cfgQueueEnable)) = 1

	// DRIVER_OK
	setStatus(statusAcknowledge | statusDriver | statusFeaturesOk | statusDriverOk)

	return true
}

func setStatus(s uint8) {
	*(*uint8)(unsafe.Pointer(dev.CommonConfigBase + cfgDeviceStatus)) = s
}

func getStatus() uint8 {
	return *(*uint8)(unsafe.Pointer(dev.CommonConfigBase + cfgDeviceStatus))
}

func writeCommon32(off uintptr, val uint32) {
	*(*uint32)(unsafe.Pointer(dev.CommonConfigBase + off)) = val
}

func readCommon32(off uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(dev.CommonConfigBase + off))
}
