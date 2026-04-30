// Package input implements VirtIO Input PCI drivers for keyboard and mouse.
//
// VirtIO Input devices (keyboard, mouse) use PCI device ID 0x1052 (0x1040 + virtio ID 18).
// Each device has two virtqueues:
//   - Queue 0 (eventq): device → driver events (8 bytes each: type u16, code u16, value u32)
//   - Queue 1 (statusq): driver → device status (LED state, unused for now)
//
// The driver pre-populates the eventq with buffers. When a key is pressed or mouse moved,
// the device fills a buffer and places it in the used ring. The IRQ handler fires softIRQFire()
// to wake any waiting userspace shepherd. The syscall handler then drains events.
package input

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
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
// Embeds virtio.PCIDevice for shared PCI transport state (Bus/Slot/Func,
// capability info, CommonConfigBase, NotifyBase, ISRBase, DeviceConfigBase).
type VirtIOInputDevice struct {
	virtio.PCIDevice // Shared PCI transport (promoted fields: Bus, Slot, Func, etc.)

	DevType             uint32 // hid.DeviceTypeKeyboard or hid.DeviceTypeMouse
	IRQNum              uint32 // GIC IRQ number
	EventQueue          virtio.VirtQueue
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
var TabletDevice *VirtIOInputDevice

// allDevices holds all discovered input devices for QueryInputDevices
var allDevices []*VirtIOInputDevice

// AllDevices returns the list of discovered input devices.
func AllDevices() []*VirtIOInputDevice {
	return allDevices
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
	dev.Notify(dev.EventQueueNotifyOff, 0)
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
// the nosplit top-half (NonTimerIRQTopHalf) now handles
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

// classifyDevice determines if this is a keyboard, mouse, or tablet based on
// the event types it supports.
func (dev *VirtIOInputDevice) classifyDevice() uint32 {
	if dev.DeviceConfigBase == 0 {
		return hid.DeviceTypeKeyboard // Default to keyboard
	}

	// Check if device supports EV_ABS (absolute positioning = tablet)
	asm.MmioWrite8(dev.DeviceConfigBase+0, VIRTIO_INPUT_CFG_EV_BITS) // select
	asm.MmioWrite8(dev.DeviceConfigBase+1, EV_ABS)                   // subsel = EV_ABS
	asm.Dsb()

	size := asm.MmioRead8(dev.DeviceConfigBase + 2)
	if size > 0 {
		return hid.DeviceTypeTablet
	}

	// Check if device supports EV_REL (relative movement = mouse)
	asm.MmioWrite8(dev.DeviceConfigBase+0, VIRTIO_INPUT_CFG_EV_BITS) // select
	asm.MmioWrite8(dev.DeviceConfigBase+1, EV_REL)                   // subsel = EV_REL
	asm.Dsb()

	size = asm.MmioRead8(dev.DeviceConfigBase + 2)
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
// Uses shared PCIDevice methods for handshake, queue enable, and status management.
func initDevice(dev *VirtIOInputDevice) bool {
	// VirtIO handshake: Reset → ACK → DRIVER → features → FEATURES_OK
	if !dev.Handshake(0, virtio.FeatureVersion1) {
		klog.Errf("[VirtIO Input] ERROR: Device rejected features\n")
		return false
	}

	// MSI-X config vector (skipped on INTx platforms where platformMSIXVector returns MSIXNoVector)
	msixVec := platformMSIXVector()
	dev.SetMSIXConfig(msixVec)

	// Queue setup using a dedicated DMA page.
	// All virtqueue structures AND event buffers live on this page with known
	// PA/VA — no Go heap, no page table walks, no cache coherency issues.
	queueSize := uint16(32) // Match NumEventBuffers
	dmaPA, dmaVA := kmem.AllocDMAPageMapped()
	if dmaPA == 0 {
		klog.Errf("[VirtIO Input] ERROR: Failed to alloc DMA page\n")
		return false
	}

	// Place virtqueue structures on the DMA page starting at offset 0
	endOff := virtio.VirtqueueInitOnDMAPage(&dev.EventQueue, queueSize, dmaPA, dmaVA, 0)
	if endOff == 0 {
		klog.Errf("[VirtIO Input] ERROR: Failed to init event queue on DMA page\n")
		return false
	}

	// Place event buffers immediately after the virtqueue structures
	eventBufOff := (endOff + 7) &^ 7 // 8-byte align for VirtIOInputEvent
	eventBufSize := uintptr(NumEventBuffers) * unsafe.Sizeof(VirtIOInputEvent{})
	if eventBufOff+eventBufSize > 4096 {
		klog.Errf("[VirtIO Input] ERROR: DMA page too small for event buffers\n")
		return false
	}
	dev.EventBuffers = (*[NumEventBuffers]VirtIOInputEvent)(unsafe.Pointer(dmaVA + eventBufOff))
	dev.EventBuffersPA = dmaPA + eventBufOff

	// Enable queue (writes addresses, reads notifyOff, assigns MSI-X vector, enables)
	notifyOff, ok := dev.EnableQueue(0, &dev.EventQueue, msixVec)
	if !ok {
		klog.Errf("[VirtIO Input] ERROR: Failed to enable event queue\n")
		return false
	}
	dev.EventQueueNotifyOff = notifyOff

	// DRIVER_OK — device is now live
	dev.SetDriverOK()
	finalStatus := dev.GetDeviceStatus()
	if finalStatus != 0x0F {
		klog.Errf("[VirtIO Input] WARNING: unexpected status=0x%x (expect 0x0F)\n", finalStatus)
	}

	// Classify device type
	dev.DevType = dev.classifyDevice()

	// Populate buffers + kick
	dev.populateEventQueue()
	dev.notifyEventQueue()

	return true
}

// gicv2mInitDone tracks whether initGICv2mSPIBase has been called.
var gicv2mInitDone bool

// initGICv2mSPIBase maps the GICv2m and reads TYPER to determine the SPI base.
// Must be called once before configureMSIX. Idempotent.
func initGICv2mSPIBase() {
	if gicv2mInitDone {
		return
	}
	gicv2mInitDone = true
	if err := kmem.MapDeviceMMIO(GICV2M_PHYS, GICV2M_SIZE); err != nil {
		klog.Errf("[VirtIO Input] ERROR: Failed to map GICv2m: %v\n", err)
		return
	}
	gicv2mBase = 0xFFFFFFFF00000000 + GICV2M_PHYS
	typer := asm.MmioRead32(gicv2mBase + GICV2M_TYPER)
	spiBase := (typer >> 16) & 0x3FF
	_ = typer & 0x3FF // spiCount — used only for validation
	nextMSIXSPI = spiBase
}

// PlatformInitInterrupts exports the platform-specific interrupt initialization
// so other drivers (block, GPU) can ensure the SPI allocator is ready before
// configuring their own MSI-X. Idempotent — safe to call multiple times.
func PlatformInitInterrupts() {
	platformInitInterrupts()
}

// allocateMSIXSPI allocates the next available GICv2m SPI number.
func allocateMSIXSPI() uint32 {
	spi := nextMSIXSPI
	nextMSIXSPI++
	return spi
}

// AllocateMSIXSPI is the public version of allocateMSIXSPI.
// Used by other VirtIO drivers (block, GPU) that configure their own MSI-X.
func AllocateMSIXSPI() uint32 {
	return allocateMSIXSPI()
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
		klog.Errf("[VirtIO Input] ERROR: No MSI-X capability found\n")
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
		klog.Errf("[VirtIO Input] MSI-X entry[0] mismatch: data=%d (expect %d) ctrl=0x%x\n",
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
	klog.Logf("[VirtIO Input] PCI scan starting\n")

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

				klog.Logf("[VirtIO Input] Found input device at PCI %d:%d.%d\n", bus, slot, funcNum)

				// Found VirtIO input device — clear Interrupt Disable before FindAndMapBARs
				cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
				cmd |= 0x7        // I/O, memory, bus master
				cmd &^= (1 << 10) // Clear Interrupt Disable
				pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

				// Configure interrupts (platform-specific: MSI-X on x86_64, INTx on ARM64/RISC-V)
				irq := platformConfigureDeviceIRQ(bus, slot, funcNum)

				dev := &VirtIOInputDevice{
					IRQNum: irq,
				}

				// Use per-device MMIO base to avoid BAR collisions with GPU/block
				mmioBase := pci.PCI_MMIO_BASE + 0x400000 + uintptr(len(allDevices))*0x10000
				if !dev.FindAndMapBARs(bus, slot, funcNum, mmioBase) {
					klog.Errf("[VirtIO Input] ERROR: Failed to find/map BARs\n")
					continue
				}

				if !initDevice(dev) {
					klog.Errf("[VirtIO Input] ERROR: Failed to initialize device\n")
					continue
				}

				// Assign to global slots
				if dev.DevType == hid.DeviceTypeKeyboard && KeyboardDevice == nil {
					KeyboardDevice = dev
					klog.Logf("[VirtIO Input] Keyboard assigned (slot %d, IRQ %d)\n", slot, irq)
				} else if dev.DevType == hid.DeviceTypeMouse && MouseDevice == nil {
					MouseDevice = dev
					klog.Logf("[VirtIO Input] Mouse assigned (slot %d, IRQ %d)\n", slot, irq)
				} else if dev.DevType == hid.DeviceTypeTablet && TabletDevice == nil {
					TabletDevice = dev
					klog.Logf("[VirtIO Input] Tablet assigned (slot %d, IRQ %d)\n", slot, irq)
				}
				allDevices = append(allDevices, dev)
			}
		}
	}
	klog.Logf("[VirtIO Input] PCI scan done: %d devices (kbd=%v mouse=%v tablet=%v)\n",
		len(allDevices), KeyboardDevice != nil, MouseDevice != nil, TabletDevice != nil)
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
