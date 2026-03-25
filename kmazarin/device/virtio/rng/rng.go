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

// rngDevice holds the state for the VirtIO RNG PCI device.
// Embeds virtio.PCIDevice for shared PCI transport state.
type rngDevice struct {
	virtio.PCIDevice // Shared PCI transport (promoted: Bus, Slot, Func, etc.)

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
	dev.Notify(dev.QueueNotifyOff, 0)

	// Poll the ISR register until the device signals completion
	for i := 0; i < 1000000; i++ {
		if dev.ReadISRStatus()&1 != 0 {
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
	const rngMMIOBase = pci.PCI_MMIO_BASE + 0x300000

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
					if !dev.FindAndMapBARs(bus, slot, funcNum, rngMMIOBase) {
						console.KPrintln("[VirtIO RNG] ERROR: Failed to find/map BARs")
						return false
					}
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
	// Feature negotiation: Reset → ACK → DRIVER → features → FEATURES_OK
	if !dev.Handshake(0, virtio.FeatureVersion1) {
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

	// Read device's max queue size and use min(16, max)
	dev.WriteCommonConfig16(virtio.CfgQueueSelect, 0)
	maxQueueSize := dev.ReadCommonConfig16(virtio.CfgQueueSize)
	queueSize := uint16(16)
	if maxQueueSize > 0 && maxQueueSize < queueSize {
		queueSize = maxQueueSize
	}

	notifyOff, ok := dev.SetupQueue(0, &dev.Queue, queueSize, queuePagePA, queuePageVA, 0, virtio.MSIXNoVector)
	if !ok {
		console.KPrintln("[VirtIO RNG] ERROR: Failed to setup queue")
		return false
	}
	dev.QueueNotifyOff = notifyOff

	// Complete handshake
	dev.SetDriverOK()

	return true
}
