// Package input implements VirtIO Input PCI drivers for keyboard and mouse.
//
// VirtIO Input devices (keyboard, mouse) use PCI device ID 0x1052 (0x1040 + virtio ID 18).
// Each device has two virtqueues:
//   - Queue 0 (eventq): device → driver events (8 bytes each: type u16, code u16, value u32)
//   - Queue 1 (statusq): driver → device status (LED state, unused for now)
//
// The driver pre-populates the eventq with buffers. When a key is pressed or mouse moved,
// the device fills a buffer and places it in the used ring. The IRQ handler fires softIRQFire()
// to wake any waiting userspace priest. The syscall handler then drains events.
package input

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

// VirtIO Input PCI device ID: 0x1040 + virtio device ID 18 = 0x1052
const VIRTIO_INPUT_DEVICE_ID = 0x1052

// VirtIO Input Config Select values (written to device config)
const (
	VIRTIO_INPUT_CFG_UNSET    = 0x00
	VIRTIO_INPUT_CFG_ID_NAME  = 0x01
	VIRTIO_INPUT_CFG_ID_DEVIDS = 0x03
	VIRTIO_INPUT_CFG_EV_BITS  = 0x11
)

// Linux evdev event types
const (
	EV_SYN = 0
	EV_KEY = 1
	EV_REL = 2
	EV_ABS = 3
)

// VirtIO PCI Common Config Register Offsets (same as GPU)
const (
	VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT = 0x00
	VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE        = 0x04
	VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT = 0x08
	VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE        = 0x0C
	VIRTIO_PCI_COMMON_CFG_MSIX_CONFIG           = 0x10
	VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS         = 0x14
	VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT          = 0x16
	VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE            = 0x18
	VIRTIO_PCI_COMMON_CFG_QUEUE_MSIX_VECTOR     = 0x1A
	VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE          = 0x1C
	VIRTIO_PCI_COMMON_CFG_QUEUE_NOTIFY_OFF      = 0x1E
	VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW        = 0x20
	VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH       = 0x24
	VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW       = 0x28
	VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH      = 0x2C
	VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW        = 0x30
	VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH       = 0x34
)

// VirtIO Device Status Bits (per VirtIO spec 1.x)
const (
	VIRTIO_STATUS_ACKNOWLEDGE = 1 << 0 // 1
	VIRTIO_STATUS_DRIVER      = 1 << 1 // 2
	VIRTIO_STATUS_DRIVER_OK   = 1 << 2 // 4
	VIRTIO_STATUS_FEATURES_OK = 1 << 3 // 8
)

// PCI config space offsets for interrupt info
const (
	PCI_INTERRUPT_LINE = 0x3C
	PCI_INTERRUPT_PIN  = 0x3D
)

// QEMU ARM virt machine maps PCI INTx to GIC SPI 3-6.
// The swizzle formula: spi = (slot + pin - 1) % 4 + 3
// GIC IRQ ID = SPI number + 32
const (
	PCIE_SPI_BASE  = 3  // First SPI number for PCI INTx
	GIC_SPI_OFFSET = 32 // SPI 0 = GIC IRQ 32
)

// GICv2m MSI controller on QEMU ARM virt
const (
	GICV2M_PHYS     = 0x08020000 // GICv2m physical base
	GICV2M_SIZE     = 0x1000     // 4KB
	GICV2M_TYPER    = 0x008      // MSI type register offset
	GICV2M_SETSPI   = 0x040      // MSI doorbell register offset
)

// gicv2mBase holds the kernel virtual address of the GICv2m after mapping.
var gicv2mBase uintptr

// MSI-X table entry structure (16 bytes per entry)
const (
	MSIX_ENTRY_ADDR_LO  = 0x00 // Message address low 32 bits
	MSIX_ENTRY_ADDR_HI  = 0x04 // Message address high 32 bits
	MSIX_ENTRY_DATA     = 0x08 // Message data (SPI number)
	MSIX_ENTRY_CONTROL  = 0x0C // Vector control (bit 0 = mask)
	MSIX_ENTRY_SIZE     = 16
)

// PCI MSI-X capability offsets
const (
	PCI_CAP_MSIX         = 0x11
	MSIX_MSGCTRL_OFFSET  = 2  // Message control at cap+2
	MSIX_TABLE_OFFSET    = 4  // Table offset/BIR at cap+4
	MSIX_PBA_OFFSET      = 8  // PBA offset/BIR at cap+8
)

// nextMSIXSPI tracks the next available GICv2m SPI for MSI-X allocation
var nextMSIXSPI uint32

// nextMSIXBarAddr tracks the next PCI MMIO address for unassigned MSI-X table BARs.
// Uses PCI_MMIO_BASE+0x80000 to avoid conflicts with common/notify BARs.
var nextMSIXBarAddr = uint32(pci.PCI_MMIO_BASE + 0x80000)

// NumEventBuffers is the number of pre-allocated event buffers per device.
const NumEventBuffers = 32

// VirtIOInputEvent matches the virtio_input_event layout (8 bytes, little-endian).
type VirtIOInputEvent struct {
	Type  uint16
	Code  uint16
	Value uint32
}

// VirtIOInputDevice holds state for one VirtIO Input PCI device.
type VirtIOInputDevice struct {
	Bus              uint8
	Slot             uint8
	Func             uint8
	DevType          uint32 // hid.DeviceTypeKeyboard or hid.DeviceTypeMouse
	IRQNum           uint32 // GIC IRQ number
	CommonConfigBase uintptr
	NotifyBase       uintptr
	ISRBase          uintptr
	DeviceConfigBase uintptr
	NotifyConfig     pci.VirtIOCapabilityInfo
	EventQueue       virtio.VirtQueue
	EventQueueNotifyOff uint16
	// Pre-allocated event buffers that descriptors point to (DMA-allocated)
	EventBuffers     *[NumEventBuffers]VirtIOInputEvent
	EventBuffersPA   uintptr // Physical address of EventBuffers for DMA
	// Track how many events have been signaled but not yet drained
	PendingEvents    uint32 // Atomic: incremented by IRQ handler
}

// Discovered devices (nil if not found)
var KeyboardDevice *VirtIOInputDevice
var MouseDevice *VirtIOInputDevice

// allDevices holds all discovered input devices for QueryInputDevices
var allDevices []*VirtIOInputDevice

// AllDevices returns the list of discovered input devices.
func AllDevices() []*VirtIOInputDevice {
	return allDevices
}


// readCommonConfig16 reads a 16-bit value from this device's common config.
//
//go:nosplit
func (dev *VirtIOInputDevice) readCommonConfig16(offset uintptr) uint16 {
	return asm.MmioRead16(dev.CommonConfigBase + offset)
}

// writeCommonConfig16 writes a 16-bit value to this device's common config.
//
//go:nosplit
func (dev *VirtIOInputDevice) writeCommonConfig16(offset uintptr, value uint16) {
	asm.MmioWrite16(dev.CommonConfigBase+offset, value)
	asm.Dsb()
}

// readCommonConfig32 reads a 32-bit value from this device's common config.
//
//go:nosplit
func (dev *VirtIOInputDevice) readCommonConfig32(offset uintptr) uint32 {
	return asm.MmioRead(dev.CommonConfigBase + offset)
}

// writeCommonConfig32 writes a 32-bit value to this device's common config.
//
//go:nosplit
func (dev *VirtIOInputDevice) writeCommonConfig32(offset uintptr, value uint32) {
	asm.MmioWrite(dev.CommonConfigBase+offset, value)
	asm.Dsb()
}

// setDeviceStatus sets the VirtIO device status.
//
//go:nosplit
func (dev *VirtIOInputDevice) setDeviceStatus(status uint8) {
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS, uint16(status))
}

// getDeviceStatus reads the VirtIO device status.
//
//go:nosplit
func (dev *VirtIOInputDevice) getDeviceStatus() uint8 {
	return uint8(dev.readCommonConfig16(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS))
}

// setupQueue configures a virtqueue in the PCI device.
//
//go:nosplit
func (dev *VirtIOInputDevice) setupQueue(queueIndex uint16, vq *virtio.VirtQueue) bool {
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, queueIndex)

	maxQueueSize := dev.readCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE)
	if vq.QueueSize > maxQueueSize {
		return false
	}

	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE, vq.QueueSize)

	// PAs were recorded by VirtqueueInit (DMA allocation)
	descPhys := vq.DescPA
	availPhys := vq.AvailPA
	usedPhys := vq.UsedPA

	_ = descPhys
	_ = availPhys
	_ = usedPhys

	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW, uint32(descPhys))
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH, uint32(descPhys>>32))
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW, uint32(availPhys))
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH, uint32(availPhys>>32))
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW, uint32(usedPhys))
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH, uint32(usedPhys>>32))

	// Readback queue addresses to verify device sees them
	rbDescLo := dev.readCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW)
	rbDescHi := dev.readCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH)
	rbDesc := uint64(rbDescHi)<<32 | uint64(rbDescLo)
	if rbDesc != descPhys {
		console.KPrintf("[VirtIO Input] ERROR: desc readback 0x%x != 0x%x\n", rbDesc, descPhys)
	}

	queueNotifyOff := dev.readCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_NOTIFY_OFF)
	if queueIndex == 0 {
		dev.EventQueueNotifyOff = queueNotifyOff
	}

	return true
}

// enableQueue enables a previously configured queue.
// Call this AFTER populating the Available ring so the device sees buffers immediately.
//
//go:nosplit
func (dev *VirtIOInputDevice) enableQueue(queueIndex uint16) {
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, queueIndex)
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE, 1)
}

// populateEventQueue pre-fills the eventq with buffers for the device to write events into.
func (dev *VirtIOInputDevice) populateEventQueue() {
	vq := &dev.EventQueue
	eventSize := uint32(unsafe.Sizeof(VirtIOInputEvent{})) // 8 bytes

	for i := 0; i < NumEventBuffers && vq.NumFree > 0; i++ {
		// PA is known: base PA + offset (event buffers live on the DMA page)
		bufPhys := uint64(dev.EventBuffersPA) + uint64(i)*uint64(eventSize)
		descIdx := virtio.VirtqueueAddDesc(vq, bufPhys, eventSize, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
		if descIdx == 0xFFFF {
			break
		}
		virtio.VirtqueueAddToAvailable(vq, descIdx)
	}

	// Flush caches for DMA coherency
	descTableSize := uintptr(vq.QueueSize) * unsafe.Sizeof(virtio.VirtQDesc{})
	asm.CleanDCacheRange(virtio.PointerToUintptr(vq.DescTable), descTableSize)
	availSize := uintptr(4 + vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(virtio.PointerToUintptr(unsafe.Pointer(vq.Available)), availSize)
	asm.DmaWmb()

}

// notifyEventQueue notifies the device that new buffers are available.
//
//go:nosplit
func (dev *VirtIOInputDevice) notifyEventQueue() {
	notifyAddr := dev.NotifyBase +
		uintptr(dev.EventQueueNotifyOff)*uintptr(dev.NotifyConfig.NotifyOffMultiplier)
	virtio.VirtqueueNotify(&dev.EventQueue, notifyAddr, 0)
}

// DrainEvents reads completed events from the used ring into buf.
// Returns the number of events drained (0 to max).
// Must be called from Go context (not IRQ context).
func (dev *VirtIOInputDevice) DrainEvents(buf []hid.HIDEvent, max int) int {
	vq := &dev.EventQueue
	count := 0

	// Targeted TLB invalidation for Used ring page + event buffer page.
	// These have been split into 4KB entries by RemapDMAPageNonCacheable,
	// so targeted TLBI works (unlike 2MB blocks in QEMU TCG).
	usedVAPage := virtio.PointerToUintptr(unsafe.Pointer(vq.Used)) &^ 0xFFF
	kmem.TlbiAndBarrier(usedVAPage)
	evtVA := virtio.PointerToUintptr(unsafe.Pointer(&dev.EventBuffers[0])) &^ 0xFFF
	kmem.TlbiAndBarrier(evtVA)

	// Also invalidate D-cache for real hardware
	usedVA := virtio.PointerToUintptr(unsafe.Pointer(vq.Used))
	usedSize := uintptr(4) + uintptr(vq.QueueSize)*8 + 2
	asm.InvalidateDCacheRange(usedVA, usedSize)
	asm.Dsb()
	asm.DmaRmb()

	// PA is known from DMA page allocation — no page table walk needed.

	for count < max && virtio.VirtqueueHasUsed(vq) {
		descIdx, _ := virtio.VirtqueueGetUsed(vq)
		if descIdx == 0xFFFF {
			break
		}

		// Invalidate event buffer cache line before reading DMA data
		if descIdx < uint32(NumEventBuffers) {
			evtVA := virtio.PointerToUintptr(unsafe.Pointer(&dev.EventBuffers[descIdx]))
			asm.InvalidateDCacheRange(evtVA, unsafe.Sizeof(VirtIOInputEvent{}))
		}
		asm.DmaRmb()

		// The descriptor index maps to our event buffer
		// (we submitted buffers 0..N-1 as descriptors 0..N-1)
		if descIdx < uint32(NumEventBuffers) {
			evt := &dev.EventBuffers[descIdx]
			buf[count] = hid.HIDEvent{
				Type:  evt.Type,
				Code:  evt.Code,
				Value: evt.Value,
			}
			count++
		}

		// Re-post this buffer for the device to reuse (PA known from DMA page)
		eventSize := uint32(unsafe.Sizeof(VirtIOInputEvent{}))
		bufPhys := uint64(dev.EventBuffersPA) + uint64(descIdx)*uint64(eventSize)
		newDescIdx := virtio.VirtqueueAddDesc(vq, bufPhys, eventSize, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
		if newDescIdx != 0xFFFF {
			virtio.VirtqueueAddToAvailable(vq, newDescIdx)
		}
	}

	if count > 0 {
		// Flush available ring changes and notify device
		availSize := uintptr(4 + vq.QueueSize*2 + 2)
		asm.CleanDCacheRange(virtio.PointerToUintptr(unsafe.Pointer(vq.Available)), availSize)
		asm.DmaWmb()
		dev.notifyEventQueue()
		atomic.StoreUint32(&dev.PendingEvents, 0)
	}

	return count
}

// irqCounter tracks total input IRQs received (for debug/test).
var irqCounter uint32

// HandleIRQ acknowledges the device interrupt via ISR read.
// Event draining and console printing are intentionally removed:
// the nosplit top-half (NonTimerIRQTopHalf / pollOneInputDev) now handles
// event delivery through per-device softIRQ ring buffers.
// DrainEvents here would race with the top-half and steal events.
func (dev *VirtIOInputDevice) HandleIRQ() {
	if dev.ISRBase != 0 {
		_ = asm.MmioRead8(dev.ISRBase)
	}
	atomic.AddUint32(&irqCounter, 1)
}

// HasPendingEvents returns true if there are events waiting to be drained.
//
//go:nosplit
func (dev *VirtIOInputDevice) HasPendingEvents() bool {
	return atomic.LoadUint32(&dev.PendingEvents) > 0 || virtio.VirtqueueHasUsed(&dev.EventQueue)
}

// irqDeviceTable maps GIC IRQ numbers to devices. No closures needed.
// Must be large enough for MSI-X SPIs (typically 112+).
var irqDeviceTable [256]*VirtIOInputDevice

// RegisterIRQDevice associates a GIC IRQ number with a device for dispatch.
func RegisterIRQDevice(irqNum uint32, dev *VirtIOInputDevice) {
	if irqNum < 256 {
		irqDeviceTable[irqNum] = dev
	}
}

// DispatchIRQ is the nosplit handler called from the IRQ dispatch table.
// It looks up the device by IRQ number and calls HandleIRQ.
//
//go:nosplit
func DispatchIRQ(irqNum uint64) {
	if irqNum >= 256 {
		return
	}
	dev := irqDeviceTable[irqNum]
	if dev == nil {
		return
	}
	dev.HandleIRQ()
}

// readDeviceName reads the device name from device config space.
// Returns the name string (may be empty if device config is not available).
func (dev *VirtIOInputDevice) readDeviceName() string {
	if dev.DeviceConfigBase == 0 {
		return ""
	}

	// Write select=VIRTIO_INPUT_CFG_ID_NAME, subsel=0
	asm.MmioWrite8(dev.DeviceConfigBase+0, VIRTIO_INPUT_CFG_ID_NAME) // select
	asm.MmioWrite8(dev.DeviceConfigBase+1, 0)                        // subsel
	asm.Dsb()

	// Read size
	size := asm.MmioRead8(dev.DeviceConfigBase + 2)
	if size == 0 || size > 128 {
		return ""
	}

	// Read name string from offset 8
	var nameBuf [128]byte
	for i := uint8(0); i < size; i++ {
		nameBuf[i] = asm.MmioRead8(dev.DeviceConfigBase + 8 + uintptr(i))
	}
	return string(nameBuf[:size])
}

// classifyDevice determines if this is a keyboard or mouse based on
// the event types it supports.
func (dev *VirtIOInputDevice) classifyDevice() uint32 {
	if dev.DeviceConfigBase == 0 {
		return hid.DeviceTypeKeyboard // Default to keyboard
	}

	// Check if device supports EV_REL (relative movement = mouse)
	asm.MmioWrite8(dev.DeviceConfigBase+0, VIRTIO_INPUT_CFG_EV_BITS) // select
	asm.MmioWrite8(dev.DeviceConfigBase+1, EV_REL)                   // subsel = EV_REL
	asm.Dsb()

	size := asm.MmioRead8(dev.DeviceConfigBase + 2)
	if size > 0 {
		return hid.DeviceTypeMouse
	}

	return hid.DeviceTypeKeyboard
}

// computeIRQ calculates the GIC IRQ number from PCI slot and interrupt pin.
// QEMU ARM virt: INTx maps to SPI 3-6, GIC IRQ = SPI + 32.
// Swizzle: spi = (slot + pin - 1) % 4 + PCIE_SPI_BASE
func computeIRQ(slot uint8, pin uint8) uint32 {
	if pin == 0 {
		return 0 // No interrupt
	}
	spi := (uint32(slot) + uint32(pin) - 1) % 4 + PCIE_SPI_BASE
	return spi + GIC_SPI_OFFSET
}

// initDevice performs the VirtIO initialization handshake for one input device.
// Matches the Linux virtio-input driver (virtinput_probe) ordering exactly:
//   1. Reset
//   2. ACKNOWLEDGE
//   3. ACKNOWLEDGE | DRIVER
//   4. Feature negotiation (accept nothing — Linux feature_table is empty)
//   5. FEATURES_OK, verify
//   6. MSI-X config vector (vp_request_msix_vectors → config_vector)
//   7. Queue setup: addresses + MSI-X vector (setup_vq → vp_active_vq)
//   8. Queue enable (vp_modern_set_queue_enable)
//   9. DRIVER_OK (virtio_device_ready) — device is now live
//  10. Classify device type (read device config)
//  11. Populate buffers + kick (virtinput_fill_evt)
func initDevice(dev *VirtIOInputDevice) bool {
	// Step 1: Reset device (Linux: virtio_reset_device in probe path)
	dev.setDeviceStatus(0)

	// Step 2-3: ACKNOWLEDGE then DRIVER (Linux: virtio_add_device status progression)
	dev.setDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE)
	dev.setDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER)

	// Step 4: Feature negotiation
	// Linux virtio-input has an EMPTY feature_table[] — it accepts NO features.
	// Read device features (both pages) but accept none.
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 0)
	devFeats0 := dev.readCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 1)
	devFeats1 := dev.readCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)
	_ = devFeats0
	_ = devFeats1

	// Accept VIRTIO_F_VERSION_1 (bit 32) — required for modern PCI transport.
	// Page 0: no device-specific features accepted.
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 0)
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 0)
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 1)
	dev.writeCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 1) // VIRTIO_F_VERSION_1

	// Step 5: FEATURES_OK (Linux: virtio_finalize_features → set_status)
	dev.setDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK)
	if (dev.getDeviceStatus() & VIRTIO_STATUS_FEATURES_OK) == 0 {
		console.KPrintln("[VirtIO Input] ERROR: Device rejected features")
		return false
	}

	// Step 6: MSI-X config vector (Linux: vp_request_msix_vectors → config_vector)
	// Tells device which MSI-X vector to use for config change notifications
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_MSIX_CONFIG, 0)
	msixCfgBack := dev.readCommonConfig16(VIRTIO_PCI_COMMON_CFG_MSIX_CONFIG)
	if msixCfgBack != 0 {
		console.KPrintf("[VirtIO Input] WARNING: msix_config readback=%d (expect 0)\n", msixCfgBack)
	}

	// Step 7: Queue setup using a dedicated DMA page.
	// Allocate a single physical page mapped with Device-nGnRnE attributes.
	// All virtqueue structures AND event buffers live on this page with known
	// PA/VA — no Go heap, no page table walks, no cache coherency issues.
	queueSize := uint16(32) // Match NumEventBuffers
	dmaPA, dmaVA := kmem.AllocDMAPageMapped()
	if dmaPA == 0 {
		console.KPrintln("[VirtIO Input] ERROR: Failed to alloc DMA page")
		return false
	}
	// Place virtqueue structures on the DMA page starting at offset 0
	endOff := virtio.VirtqueueInitOnDMAPage(&dev.EventQueue, queueSize, dmaPA, dmaVA, 0)
	if endOff == 0 {
		console.KPrintln("[VirtIO Input] ERROR: Failed to init event queue on DMA page")
		return false
	}

	// Place event buffers immediately after the virtqueue structures
	eventBufOff := (endOff + 7) &^ 7 // 8-byte align for VirtIOInputEvent
	eventBufSize := uintptr(NumEventBuffers) * unsafe.Sizeof(VirtIOInputEvent{})
	if eventBufOff+eventBufSize > 4096 {
		console.KPrintln("[VirtIO Input] ERROR: DMA page too small for event buffers")
		return false
	}
	dev.EventBuffers = (*[NumEventBuffers]VirtIOInputEvent)(unsafe.Pointer(dmaVA + eventBufOff))
	dev.EventBuffersPA = dmaPA + eventBufOff

	if !dev.setupQueue(0, &dev.EventQueue) {
		console.KPrintln("[VirtIO Input] ERROR: Failed to setup event queue")
		return false
	}
	// Assign MSI-X vector 0 to queue 0 (Linux: vp_active_vq writes queue_msix_vector)
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, 0)
	dev.writeCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_MSIX_VECTOR, 0)
	msixVecBack := dev.readCommonConfig16(VIRTIO_PCI_COMMON_CFG_QUEUE_MSIX_VECTOR)
	if msixVecBack == 0xFFFF {
		console.KPrintln("[VirtIO Input] WARNING: queue_msix_vector rejected (0xFFFF)")
	}

	// Step 8: Queue enable (Linux: vp_modern_set_queue_enable after all queues set up)
	dev.enableQueue(0)

	// Step 9: DRIVER_OK (Linux: virtio_device_ready — device is now live)
	dev.setDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK | VIRTIO_STATUS_DRIVER_OK)
	finalStatus := dev.getDeviceStatus()
	if finalStatus != 0x0F {
		console.KPrintf("[VirtIO Input] WARNING: unexpected status=0x%x (expect 0x0F)\n", finalStatus)
	}

	// Step 10: Classify device type (Linux reads config between device_ready and fill_evt)
	dev.DevType = dev.classifyDevice()

	// Step 11: Populate buffers + kick (Linux: virtinput_fill_evt)
	dev.populateEventQueue()
	notifyAddr := dev.NotifyBase +
		uintptr(dev.EventQueueNotifyOff)*uintptr(dev.NotifyConfig.NotifyOffMultiplier)
	dev.notifyEventQueue()

	// Diagnostic: verify queue state after init
	vq := &dev.EventQueue
	usedVA := virtio.PointerToUintptr(unsafe.Pointer(vq.Used))
	console.KPrintf("[VirtIO Input %d:%d.%d] Q0: availIdx=%d usedIdx=%d numFree=%d\n",
		dev.Bus, dev.Slot, dev.Func, vq.Available.Idx, asm.MmioRead16(usedVA+2), vq.NumFree)
	console.KPrintf("[VirtIO Input %d:%d.%d] notifyBase=0x%x notifyOff=%d mult=%d addr=0x%x\n",
		dev.Bus, dev.Slot, dev.Func, dev.NotifyBase, dev.EventQueueNotifyOff,
		dev.NotifyConfig.NotifyOffMultiplier, notifyAddr)
	console.KPrintf("[VirtIO Input %d:%d.%d] descPA=0x%x availPA=0x%x usedPA=0x%x evtPA=0x%x\n",
		dev.Bus, dev.Slot, dev.Func,
		vq.DescPA, vq.AvailPA, vq.UsedPA, dev.EventBuffersPA)

	return true
}

// initGICv2mSPIBase maps the GICv2m and reads TYPER to determine the SPI base.
// Must be called once before configureMSIX.
func initGICv2mSPIBase() {
	if err := kmem.MapDeviceMMIO(GICV2M_PHYS, GICV2M_SIZE); err != nil {
		console.KPrintf("[VirtIO Input] ERROR: Failed to map GICv2m: %v\n", err)
		return
	}
	gicv2mBase = 0xFFFFFFFF00000000 + GICV2M_PHYS
	typer := asm.MmioRead32(gicv2mBase + GICV2M_TYPER)
	spiBase := (typer >> 16) & 0x3FF
	_ = typer & 0x3FF // spiCount
	nextMSIXSPI = spiBase
}

// allocateMSIXSPI allocates the next available GICv2m SPI number.
func allocateMSIXSPI() uint32 {
	spi := nextMSIXSPI
	nextMSIXSPI++
	return spi
}

// configureMSIX finds the MSI-X capability, programs MSI-X table entries
// to target the GICv2m doorbell, and enables MSI-X.
// Returns the GIC IRQ number (SPI + 32) for this device, or 0 on failure.
func configureMSIX(bus, slot, funcNum uint8) uint32 {
	// Find MSI-X capability
	capPtr := pci.ConfigRead8(bus, slot, funcNum, pci.PCI_CAPABILITIES)
	var msixCapPtr uint8
	for i := 0; i < 32 && capPtr != 0 && capPtr != 0xFF; i++ {
		capID := pci.ConfigRead8(bus, slot, funcNum, capPtr)
		if capID == PCI_CAP_MSIX {
			msixCapPtr = capPtr
			break
		}
		capPtr = pci.ConfigRead8(bus, slot, funcNum, capPtr+1)
	}
	if msixCapPtr == 0 {
		console.KPrintln("[VirtIO Input] ERROR: No MSI-X capability found")
		return 0
	}

	// Read message control to get table size
	msgCtrlWord := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr)
	msgCtrl := uint16(msgCtrlWord >> 16)
	tableSize := (msgCtrl & 0x7FF) + 1 // Bits [10:0] = table size - 1

	// Read table BAR and offset
	tableOffsetBIR := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr+MSIX_TABLE_OFFSET)
	tableBIR := tableOffsetBIR & 0x7            // BAR index
	tableOffset := tableOffsetBIR & ^uint32(0x7) // Offset within BAR

	// Always reprogram the MSI-X table BAR to a unique address from our pool.
	// UEFI-assigned addresses can collide with BARs reprogrammed by other drivers
	// (e.g., keyboard BAR4 reprogrammed to 0x10040000 overlaps mouse's UEFI BAR1 at 0x10041000).
	barOffset := uint8(0x10 + tableBIR*4)
	barAddr := pci.ConfigRead32(bus, slot, funcNum, barOffset)
	is64bit := barAddr&0x6 == 0x4

	pciAddr := nextMSIXBarAddr
	nextMSIXBarAddr += 0x1000
	pci.ConfigWrite32(bus, slot, funcNum, barOffset, pciAddr)
	if is64bit {
		pci.ConfigWrite32(bus, slot, funcNum, barOffset+4, 0) // Clear high 32 bits
	}
	barAddr = pci.ConfigRead32(bus, slot, funcNum, barOffset)
	barBasePA := uintptr(barAddr & 0xFFFFFFF0)

	// Map MSI-X BAR into TTBR1 kernel space
	kmem.MapDeviceMMIO(barBasePA, 0x1000)
	barBase := barBasePA + constants.KernelMMIOOffset

	tableBase := barBase + uintptr(tableOffset)

	// Allocate an SPI and program all vectors to the same SPI
	spi := allocateMSIXSPI()
	gicIRQ := spi + GIC_SPI_OFFSET

	// Enable MSI-X with function mask so we can safely program the table
	low16 := msgCtrlWord & 0xFFFF
	enableAndMask := uint32(msgCtrl) | (1 << 15) | (1 << 14) // enable + function mask
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableAndMask<<16))

	// Program all table entries to point to GICv2m doorbell.
	// MSI-X data must contain the SPI number (not the GIC IRQ number).
	// The GICv2m SETSPI register maps SPI N → GIC IRQ N+32 internally.
	doorbellAddr := uint64(GICV2M_PHYS + GICV2M_SETSPI)
	for i := uint32(0); i < uint32(tableSize); i++ {
		entryAddr := tableBase + uintptr(i*MSIX_ENTRY_SIZE)
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_LO, uint32(doorbellAddr))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_ADDR_HI, uint32(doorbellAddr>>32))
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_DATA, gicIRQ) // GICv2m expects SPI+32
		asm.MmioWrite32(entryAddr+MSIX_ENTRY_CONTROL, 0) // Unmask
	}
	asm.Dsb()

	// Readback MSI-X table entry 0 to verify programming
	readData := asm.MmioRead32(tableBase + MSIX_ENTRY_DATA)
	readCtrl := asm.MmioRead32(tableBase + MSIX_ENTRY_CONTROL)
	if readData != gicIRQ || readCtrl != 0 {
		console.KPrintf("[VirtIO Input] MSI-X entry[0] mismatch: data=%d (expect %d) ctrl=0x%x\n",
			readData, gicIRQ, readCtrl)
	}

	// Clear function mask (keep enable)
	enableOnly := (uint32(msgCtrl) | (1 << 15)) &^ (1 << 14) // enable=1, function mask=0
	pci.ConfigWrite32(bus, slot, funcNum, msixCapPtr, low16|(enableOnly<<16))

	// Verify MSI-X is actually enabled in PCI config
	finalMsgCtrl := pci.ConfigRead32(bus, slot, funcNum, msixCapPtr)
	finalCtrl := uint16(finalMsgCtrl >> 16)
	msixEnabled := (finalCtrl & (1 << 15)) != 0
	funcMasked := (finalCtrl & (1 << 14)) != 0
	readAddr := asm.MmioRead32(tableBase + MSIX_ENTRY_ADDR_LO)
	readDataFinal := asm.MmioRead32(tableBase + MSIX_ENTRY_DATA)
	_ = msixEnabled
	_ = funcMasked
	_ = readAddr
	_ = readDataFinal
	return gicIRQ
}

// InitVirtIOInput discovers and initializes all VirtIO Input PCI devices.
// Should be called during kernel init after PCI ECAM is available.
func InitVirtIOInput() {
	platformInitInterrupts()

	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				fullReg := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_VENDOR_ID)
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				if vendorID == 0xFFFF || vendorID == 0 {
					if funcNum == 0 && vendorID == 0xFFFF {
						break // No device at this slot
					}
					continue
				}

				if vendorID != pci.VIRTIO_VENDOR_ID || deviceID != VIRTIO_INPUT_DEVICE_ID {
					continue
				}

				// Found VirtIO input device

				// Enable device
				cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
				cmd |= 0x7        // I/O, memory, bus master
				cmd &^= (1 << 10) // Clear Interrupt Disable
				pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

				// Configure interrupts (platform-specific: MSI-X on ARM64, polling on x86_64)
				irq := platformConfigureDeviceIRQ(bus, slot, funcNum)

				// Find VirtIO capabilities
				var common, notify, isr, deviceCfg pci.VirtIOCapabilityInfo
				if !pci.FindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &deviceCfg) {
					console.KPrintln("[VirtIO Input] ERROR: No VirtIO capabilities found")
					continue
				}

				// Read and program BAR for common config (handles 64-bit BARs)
				barBasePA := pci.ReadBAR64(bus, slot, funcNum, common.Bar)
				if barBasePA == 0 || barBasePA >= 0x100000000 {
					// Program BAR to PCI MMIO window, offset to avoid GPU/Block conflicts
					pciAddr := pci.PCI_MMIO_BASE + 0x40000 + uintptr(len(allDevices))*0x10000
					pci.WriteBAR64(bus, slot, funcNum, common.Bar, pciAddr)
					barBasePA = pci.ReadBAR64(bus, slot, funcNum, common.Bar)
				}
				// Map PCI BAR into TTBR1 kernel space so we don't rely on TTBR0
				kmem.MapDeviceMMIO(barBasePA, 0x10000) // Map 64KB for the BAR range
				barBase := barBasePA + constants.KernelMMIOOffset

				// Use platform-configured IRQ (MSI-X on ARM64, 0 for polling on x86_64)
				irqNum := irq

				dev := &VirtIOInputDevice{
					Bus:              bus,
					Slot:             slot,
					Func:             funcNum,
					IRQNum:           irqNum,
					CommonConfigBase: barBase + uintptr(common.OffsetInBar),
					NotifyConfig:     notify,
				}

				// Notify BAR (handles 64-bit BARs)
				notifyBarPA := pci.ReadBAR64(bus, slot, funcNum, notify.Bar)
				if notifyBarPA >= 0x100000000 {
					// Reprogram if above 4GB — but only if different from common BAR
					// (same BAR would have been reprogrammed already)
					notifyBarPA = barBasePA // Assume same BAR
				}
				if notifyBarPA == barBasePA {
					// Same BAR, reuse barBase
					dev.NotifyBase = barBase + uintptr(notify.OffsetInBar)
				} else {
					kmem.MapDeviceMMIO(notifyBarPA, 0x10000)
					dev.NotifyBase = notifyBarPA + constants.KernelMMIOOffset + uintptr(notify.OffsetInBar)
				}

				// ISR BAR (handles 64-bit BARs)
				if isr.Offset != 0 {
					isrBarPA := pci.ReadBAR64(bus, slot, funcNum, isr.Bar)
					kmem.MapDeviceMMIO(isrBarPA, 0x10000)
					dev.ISRBase = isrBarPA + constants.KernelMMIOOffset + uintptr(isr.OffsetInBar)
				}

				// Device config BAR (handles 64-bit BARs)
				if deviceCfg.Offset != 0 {
					devCfgBarPA := pci.ReadBAR64(bus, slot, funcNum, deviceCfg.Bar)
					kmem.MapDeviceMMIO(devCfgBarPA, 0x10000)
					dev.DeviceConfigBase = devCfgBarPA + constants.KernelMMIOOffset + uintptr(deviceCfg.OffsetInBar)
				}

				if !initDevice(dev) {
					console.KPrintln("[VirtIO Input] ERROR: Failed to initialize device")
					continue
				}

				name := dev.readDeviceName()
				typeName := "keyboard"
				if dev.DevType == hid.DeviceTypeMouse {
					typeName = "mouse"
				}
				_ = name
			_ = typeName

				// Assign to global slots
				if dev.DevType == hid.DeviceTypeKeyboard && KeyboardDevice == nil {
					KeyboardDevice = dev
				} else if dev.DevType == hid.DeviceTypeMouse && MouseDevice == nil {
					MouseDevice = dev
				}
				allDevices = append(allDevices, dev)
			}
		}
	}

}

// PollAllDevices checks the Used ring of all input devices for pending events.
// This is a FALLBACK for when interrupts are not working. With MSI-X wired
// to the GIC, the interrupt path (HandleIRQ) should fire instead.
// Kept for debugging but not called in normal operation.
func PollAllDevices() {
	for _, dev := range allDevices {
		if dev == nil {
			continue
		}
		if virtio.VirtqueueHasUsed(&dev.EventQueue) {
			dev.HandleIRQ()
			if softIRQFireFunc != nil {
				softIRQFireFunc(dev.IRQNum)
			}
		}
	}
}

// softIRQFireFunc is set by the kernel to avoid circular imports.
// It calls SoftIRQSlotFire(irqNum).
var softIRQFireFunc func(uint32)

// SetSoftIRQFireFunc sets the callback for firing soft IRQ slots.
func SetSoftIRQFireFunc(f func(uint32)) {
	softIRQFireFunc = f
}
