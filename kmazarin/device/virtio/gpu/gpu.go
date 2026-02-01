
package gpu

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/pci"
	"mazzy/shared/constants"
	"unsafe"
)

// VirtIO GPU Constants

// VirtIO GPU Command Types
const (
	VIRTIO_GPU_CMD_GET_DISPLAY_INFO        = 0x0100
	VIRTIO_GPU_CMD_RESOURCE_CREATE_2D      = 0x0101
	VIRTIO_GPU_CMD_RESOURCE_UNREF          = 0x0102
	VIRTIO_GPU_CMD_SET_SCANOUT             = 0x0103
	VIRTIO_GPU_CMD_RESOURCE_FLUSH          = 0x0104
	VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D     = 0x0105
	VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING = 0x0106
	VIRTIO_GPU_CMD_RESOURCE_DETACH_BACKING = 0x0107
)

// VirtIO GPU Response Types
const (
	VIRTIO_GPU_RESP_OK_NODATA            = 0x1100
	VIRTIO_GPU_RESP_OK_DISPLAY_INFO      = 0x1101
	VIRTIO_GPU_RESP_ERR_UNSPEC           = 0x1200
	VIRTIO_GPU_RESP_ERR_OUT_OF_MEMORY    = 0x1201
	VIRTIO_GPU_RESP_ERR_INVALID_SCANOUT  = 0x1202
	VIRTIO_GPU_RESP_ERR_INVALID_RESOURCE = 0x1203
	VIRTIO_GPU_RESP_ERR_INVALID_CONTEXT  = 0x1204
)

// VirtIO GPU Pixel Formats
const (
	VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM = 1
	VIRTIO_GPU_FORMAT_B8G8R8X8_UNORM = 2
	VIRTIO_GPU_FORMAT_R8G8B8A8_UNORM = 3
)

// VirtIO PCI Common Config Register Offsets
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
	VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW       = 0x28
	VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH      = 0x2C
	VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW        = 0x30
	VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH       = 0x34
)

// VirtIO Device Status Bits
const (
	VIRTIO_STATUS_ACKNOWLEDGE        = 1 << 0
	VIRTIO_STATUS_DRIVER             = 1 << 1
	VIRTIO_STATUS_FAILED             = 1 << 2
	VIRTIO_STATUS_FEATURES_OK        = 1 << 3
	VIRTIO_STATUS_DRIVER_OK          = 1 << 4
	VIRTIO_STATUS_DEVICE_NEEDS_RESET = 1 << 6
)

// VirtIO GPU Command Structures

// VirtIOGPUCtrlHdr is the header for all VirtIO GPU commands
type VirtIOGPUCtrlHdr struct {
	Type    uint32 // Command type
	Flags   uint32 // Command flags
	FenceID uint64 // Fence ID for synchronization
	CtxID   uint32 // Context ID
	Padding uint32 // Padding
}

// VirtIOGPUResourceCreate2D creates a 2D resource
type VirtIOGPUResourceCreate2D struct {
	Hdr        VirtIOGPUCtrlHdr
	ResourceID uint32 // Unique resource ID
	Format     uint32 // Pixel format (VIRTIO_GPU_FORMAT_*)
	Width      uint32 // Width in pixels
	Height     uint32 // Height in pixels
}

// VirtIOGPUMemEntry describes a memory region for backing store
type VirtIOGPUMemEntry struct {
	Addr uint64 // Physical address
	Len  uint32 // Length in bytes
}

// VirtIOGPUResourceAttachBacking attaches backing store to a resource
type VirtIOGPUResourceAttachBacking struct {
	Hdr        VirtIOGPUCtrlHdr
	ResourceID uint32 // Resource ID
	NrEntries  uint32 // Number of memory entries
	// Followed by array of VirtIOGPUMemEntry
}

// VirtIOGPURect describes a rectangle
type VirtIOGPURect struct {
	X      uint32 // X coordinate
	Y      uint32 // Y coordinate
	Width  uint32 // Width
	Height uint32 // Height
}

// VirtIOGPUSetScanout sets scanout (connects resource to display)
type VirtIOGPUSetScanout struct {
	Hdr        VirtIOGPUCtrlHdr
	Rect       VirtIOGPURect // Rectangle
	ScanoutID  uint32        // Scanout ID (usually 0)
	ResourceID uint32        // Resource ID
}

// VirtIOGPUTransferToHost2D transfers data to host (updates display)
type VirtIOGPUTransferToHost2D struct {
	Hdr        VirtIOGPUCtrlHdr
	Rect       VirtIOGPURect // Region to transfer
	Offset     uint64        // Offset in resource
	ResourceID uint32        // Resource ID
	Padding    uint32        // Padding
}

// VirtIOGPUResourceFlush requests the host to flush a resource to display
type VirtIOGPUResourceFlush struct {
	Hdr        VirtIOGPUCtrlHdr
	Rect       VirtIOGPURect // Region to flush
	ResourceID uint32        // Resource ID
	Padding    uint32        // Padding
}

// VirtIOGPUDevice holds VirtIO GPU device state
type VirtIOGPUDevice struct {
	Bus              uint8
	Slot             uint8
	Func             uint8
	CommonConfig     pci.VirtIOCapabilityInfo // Common Config capability
	NotifyConfig     pci.VirtIOCapabilityInfo // Notify Config capability
	ISRConfig        pci.VirtIOCapabilityInfo // ISR Config capability
	DeviceConfig     pci.VirtIOCapabilityInfo // Device Config capability
	CommonConfigBase uintptr                  // MMIO base for common config
	NotifyBase       uintptr                  // MMIO base for notify
	ControlQueue          virtio.VirtQueue // Control queue for GPU commands
	ControlQueueNotifyOff uint16           // Notify offset for control queue
	ResourceID            uint32           // Current resource ID
	Framebuffer      unsafe.Pointer       // Framebuffer memory
	FramebufferSize  uint32               // Framebuffer size in bytes
	Width            uint32               // Framebuffer width in pixels
	Height           uint32               // Framebuffer height in pixels
	Pitch            uint32               // Bytes per row
}

var virtioGPUDevice VirtIOGPUDevice

// Framebuffer is at fixed physical address 0x41000000 (8 MB)
// Defined in constants/layout.go
const virtioGPUFramebufferAddr = constants.FramebufferPhysAddr
const virtioGPUFramebufferSize = constants.FramebufferSize

// Static buffer for attach backing command (small, avoids kmalloc)
var virtioGPUAttachCmdBuf [unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{})]byte

// virtioPCIReadCommonConfig reads a 16-bit value from VirtIO PCI common config
//
//go:nosplit
func virtioPCIReadCommonConfig(offset uintptr) uint16 {
	base := virtioGPUDevice.CommonConfigBase
	return asm.MmioRead16(base + offset)
}

// virtioPCIWriteCommonConfig writes a 16-bit value to VirtIO PCI common config
//
//go:nosplit
func virtioPCIWriteCommonConfig(offset uintptr, value uint16) {
	base := virtioGPUDevice.CommonConfigBase
	asm.MmioWrite16(base+offset, value)
	asm.Dsb()
}

// virtioPCIReadCommonConfig32 reads a 32-bit value from VirtIO PCI common config
//
//go:nosplit
func virtioPCIReadCommonConfig32(offset uintptr) uint32 {
	base := virtioGPUDevice.CommonConfigBase
	return asm.MmioRead(base + offset)
}

// virtioPCIWriteCommonConfig32 writes a 32-bit value to VirtIO PCI common config
//
//go:nosplit
func virtioPCIWriteCommonConfig32(offset uintptr, value uint32) {
	base := virtioGPUDevice.CommonConfigBase
	asm.MmioWrite(base+offset, value)
	asm.Dsb()
}

// virtioPCISetDeviceStatus sets the device status
//
//go:nosplit
func virtioPCISetDeviceStatus(status uint8) {
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS, uint16(status))
}

// virtioPCIGetDeviceStatus gets the device status
//
//go:nosplit
func virtioPCIGetDeviceStatus() uint8 {
	return uint8(virtioPCIReadCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS))
}

// virtioPCISetupQueue sets up a virtqueue
//
//go:nosplit
func virtioPCISetupQueue(queueIndex uint16, vq *virtio.VirtQueue) bool {
	// Select queue
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, queueIndex)

	// Read max queue size from device
	maxQueueSize := virtioPCIReadCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE)

	// Fail fast if device max is smaller than requested
	// We can't safely clamp after VirtqueueInit because NumFree and the free list
	// are already sized for the original queue size
	if vq.QueueSize > maxQueueSize {
		console.KPrintf("[VirtIO GPU] Queue size %d exceeds device max %d\n",
			vq.QueueSize, maxQueueSize)
		return false
	}

	// Set queue size
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE, vq.QueueSize)

	// Get physical addresses and configure queue
	descPhys := virtio.VirtqueueGetPhysicalAddr(vq.DescTable)
	availPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(vq.Available))
	usedPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(vq.Used))

	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW, uint32(descPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH, uint32(descPhys>>32))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW, uint32(availPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH, uint32(availPhys>>32))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW, uint32(usedPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH, uint32(usedPhys>>32))

	// Read queue_notify_off for this queue
	queueNotifyOff := virtioPCIReadCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_NOTIFY_OFF)
	if queueIndex == 0 {
		virtioGPUDevice.ControlQueueNotifyOff = queueNotifyOff
	}

	// Enable queue
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE, 1)

	return true
}

// findVirtIOGPU finds the VirtIO GPU PCI device
// Returns true if found, false otherwise
//
//go:nosplit
func findVirtIOGPU() bool {
	// Debug output commented out


	// Scan PCI bus
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor/device ID
				fullReg := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_VENDOR_ID)
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				// Check if device exists
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Debug: show all found devices
				// uartPutsDirect("VirtIO GPU: Found device - bus=")
				// uartPutHex8Direct(bus)
				// uartPutsDirect(" slot=")
				// uartPutHex8Direct(slot)
				// uartPutsDirect(" func=")
				// uartPutHex8Direct(funcNum)
				// uartPutsDirect(" vendor=")
				// uartPutHex16Direct(uint16(vendorID))
				// uartPutsDirect(" device=")
				// uartPutHex16Direct(uint16(deviceID))
				// Debug output commented out

				// Check if this is VirtIO GPU
				if vendorID == pci.VIRTIO_VENDOR_ID && deviceID == pci.VIRTIO_GPU_DEVICE_ID {
					// Enable device
					cmd := pci.ConfigRead32(bus, slot, funcNum, pci.PCI_COMMAND)
					cmd |= 0x7 // Enable I/O, memory, bus master
					pci.ConfigWrite32(bus, slot, funcNum, pci.PCI_COMMAND, cmd)

					// Find VirtIO capabilities
					var common, notify, isr, device pci.VirtIOCapabilityInfo
					if !pci.FindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &device) {
						return false
					}

					// Read BAR for common config
					barOffset := 0x10 + common.Bar*4 // BAR0 = 0x10, BAR1 = 0x14, etc.
					bar := pci.ConfigRead32(bus, slot, funcNum, uint8(barOffset))

					// Check if BAR needs programming (base address is 0)
					if (bar & 0xFFFFFFF0) == 0 {
						// Program BAR to use PCI MMIO space at 0x10000000
						const PCI_MMIO_BASE = uint32(0x10000000)
						pci.ConfigWrite32(bus, slot, funcNum, uint8(barOffset), PCI_MMIO_BASE)

						// If it's a 64-bit BAR, also program the high 32 bits
						if (bar & 0x6) == 0x4 {
							pci.ConfigWrite32(bus, slot, funcNum, uint8(barOffset+4), 0)
						}

						bar = pci.ConfigRead32(bus, slot, funcNum, uint8(barOffset))
					}

					barBase := uintptr(bar & 0xFFFFFFF0)

					// Map BAR MMIO into kernel high-memory (TTBR1)
					kmem.MapDeviceMMIO(barBase, 0x10000)

					virtioGPUDevice.Bus = bus
					virtioGPUDevice.Slot = slot
					virtioGPUDevice.Func = funcNum
					virtioGPUDevice.CommonConfig = common
					virtioGPUDevice.NotifyConfig = notify
					virtioGPUDevice.ISRConfig = isr
					virtioGPUDevice.DeviceConfig = device
					virtioGPUDevice.CommonConfigBase = barBase + constants.KernelMMIOOffset + uintptr(common.OffsetInBar)

					// Calculate notify base
					notifyBarOffset := 0x10 + notify.Bar*4
					notifyBar := pci.ConfigRead32(bus, slot, funcNum, uint8(notifyBarOffset))
					notifyBarBase := uintptr(notifyBar & 0xFFFFFFF0)
					if notifyBarBase != barBase {
						kmem.MapDeviceMMIO(notifyBarBase, 0x10000)
					}
					virtioGPUDevice.NotifyBase = notifyBarBase + constants.KernelMMIOOffset + uintptr(notify.OffsetInBar)

					return true
				}
			}
		}
	}

	return false
}

// virtioGPUInit initializes the VirtIO GPU device
// Returns true on success, false on failure
//
//go:nosplit
func virtioGPUInit() bool {
	// Reset device
	virtioPCISetDeviceStatus(0)

	// Acknowledge and indicate driver present
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE)
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER)

	// Feature negotiation - read device features (not used, just for completeness)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 0)
	_ = virtioPCIReadCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE_SELECT, 1)
	_ = virtioPCIReadCommonConfig32(VIRTIO_PCI_COMMON_CFG_DEVICE_FEATURE)

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
		console.KPrintln("[VirtIO GPU] ERROR: Device rejected features")
		return false
	}

	// Initialize control queue
	queueSize := uint16(64)
	if !virtio.VirtqueueInit(&virtioGPUDevice.ControlQueue, queueSize) {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to init control queue")
		return false
	}

	// Setup queue in device
	if !virtioPCISetupQueue(0, &virtioGPUDevice.ControlQueue) {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to setup queue")
		return false
	}

	// Driver OK
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK | VIRTIO_STATUS_DRIVER_OK)

	// Check if FAILED bit is set
	finalStatus := virtioPCIGetDeviceStatus()
	if (finalStatus & VIRTIO_STATUS_FAILED) != 0 {
		console.KPrintln("[VirtIO GPU] ERROR: Device failed")
		return false
	}

	virtioGPUDevice.ResourceID = 1
	return true
}

// virtioGPUSendCommand sends a GPU command via the control queue
// Returns response type, or 0xFFFF on error
//
//go:nosplit
func virtioGPUSendCommand(cmdBuf unsafe.Pointer, cmdSize uint32, respBuf unsafe.Pointer, respSize uint32) uint32 {
	vq := &virtioGPUDevice.ControlQueue

	// Allocate descriptors for command and response
	cmdPhys := virtio.VirtqueueGetPhysicalAddr(cmdBuf)
	cmdDescIdx := virtio.VirtqueueAddDesc(vq, cmdPhys, cmdSize, 0, 0xFFFF)
	if cmdDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to allocate cmd descriptor")
		return 0xFFFF
	}

	respPhys := virtio.VirtqueueGetPhysicalAddr(respBuf)
	respDescIdx := virtio.VirtqueueAddDesc(vq, respPhys, respSize, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if respDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to allocate resp descriptor")
		// Free the already-allocated cmd descriptor to avoid leak
		virtio.VirtqueueFreeDescChain(vq, cmdDescIdx)
		return 0xFFFF
	}

	// Link descriptors
	var descSize uintptr = unsafe.Sizeof(virtio.VirtQDesc{})
	cmdDescPtr := virtio.CastToPointer[virtio.VirtQDesc](virtio.PointerToUintptr(vq.DescTable) + uintptr(cmdDescIdx)*descSize)
	cmdDescPtr.Flags |= virtio.VIRTQ_DESC_F_NEXT
	cmdDescPtr.Next = respDescIdx

	// Add to available ring
	virtio.VirtqueueAddToAvailable(vq, cmdDescIdx)

	// Cache maintenance for DMA coherency
	descTableSize := uintptr(vq.QueueSize) * unsafe.Sizeof(virtio.VirtQDesc{})
	descTableAddr := virtio.PointerToUintptr(vq.DescTable)
	asm.CleanDCacheRange(descTableAddr, descTableSize)

	availSize := uintptr(4 + vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(virtio.PointerToUintptr(unsafe.Pointer(vq.Available)), availSize)

	// DMA write barrier
	asm.DmaWmb()

	// Notify device
	queueIndex := uint16(0) // Control queue
	queueNotifyAddr := virtioGPUDevice.NotifyBase +
		uintptr(virtioGPUDevice.ControlQueueNotifyOff)*uintptr(virtioGPUDevice.NotifyConfig.NotifyOffMultiplier)
	virtio.VirtqueueNotify(vq, queueNotifyAddr, queueIndex)

	// Poll for response
	maxWait := 1000000
	waited := 0
	for !virtio.VirtqueueHasUsed(vq) && waited < maxWait {
		for delay := 0; delay < 100; delay++ {
		}
		waited++
	}

	if waited >= maxWait {
		console.KPrintln("[VirtIO GPU] ERROR: Timeout waiting for response")
		return 0xFFFF
	}

	// DMA read barrier - ensure device writes are visible
	asm.DmaRmb()

	// Get response
	usedDescIdx, _ := virtio.VirtqueueGetUsed(vq)
	if usedDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to get used descriptor")
		return 0xFFFF
	}

	// Free descriptor chain
	virtio.VirtqueueFreeDescChain(vq, uint16(usedDescIdx))

	// DMA read barrier before reading response buffer
	asm.DmaRmb()

	// Read response type from response buffer
	respHdr := (*VirtIOGPUCtrlHdr)(respBuf)
	return respHdr.Type
}

// virtioGPUGetDisplayInfo queries the GPU for display information
// This is a simple test command to verify the command channel works
func virtioGPUGetDisplayInfo() bool {
	// Prepare GET_DISPLAY_INFO command
	var cmd VirtIOGPUCtrlHdr
	cmd.Type = VIRTIO_GPU_CMD_GET_DISPLAY_INFO
	cmd.Flags = 0
	cmd.FenceID = 0
	cmd.CtxID = 0
	cmd.Padding = 0

	// Response buffer (header + display info)
	var resp [384]byte // VirtIOGPUCtrlHdr (24 bytes) + display info (360 bytes max)

	console.KPrintf("[VirtIO GPU] Sending GET_DISPLAY_INFO (type=0x%x)\n", cmd.Type)

	respType := virtioGPUSendCommand(
		unsafe.Pointer(&cmd),
		uint32(unsafe.Sizeof(cmd)),
		unsafe.Pointer(&resp[0]),
		uint32(len(resp)))

	return respType == VIRTIO_GPU_RESP_OK_DISPLAY_INFO
}

// virtioGPUSetupFramebuffer sets up the framebuffer using VirtIO GPU
// Returns true on success, false on failure
//
func virtioGPUSetupFramebuffer(width, height uint32) bool {
	// Use dedicated framebuffer region at fixed address
	fbSize := width * height * 4 // 4 bytes per pixel (BGRA8888)

	if fbSize > virtioGPUFramebufferSize {
		console.KPrintln("[VirtIO GPU] ERROR: Framebuffer size too large")
		return false
	}

	// Zero framebuffer
	fbMem := unsafe.Pointer(uintptr(virtioGPUFramebufferAddr))
	virtio.Bzero4K(fbMem, fbSize)

	virtioGPUDevice.Framebuffer = fbMem
	virtioGPUDevice.FramebufferSize = fbSize
	virtioGPUDevice.Width = width
	virtioGPUDevice.Height = height
	virtioGPUDevice.Pitch = width * 4

	// Step 1: Create 2D resource
	var createCmd VirtIOGPUResourceCreate2D
	createCmd.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_CREATE_2D
	createCmd.ResourceID = virtioGPUDevice.ResourceID
	createCmd.Format = VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM
	createCmd.Width = width
	createCmd.Height = height

	var createResp VirtIOGPUCtrlHdr

	respType := virtioGPUSendCommand(unsafe.Pointer(&createCmd), uint32(unsafe.Sizeof(createCmd)), unsafe.Pointer(&createResp), uint32(unsafe.Sizeof(createResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: CREATE_2D failed (0x%04x)\n", respType)
		return false
	}

	// Step 2: Attach backing store
	attachCmdSize := uint32(unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{}))
	attachCmdBuf := unsafe.Pointer(&virtioGPUAttachCmdBuf[0])

	// Set up command structure
	cmdPtr := virtio.CastToPointer[VirtIOGPUResourceAttachBacking](virtio.PointerToUintptr(attachCmdBuf))
	cmdPtr.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING
	cmdPtr.ResourceID = virtioGPUDevice.ResourceID
	cmdPtr.NrEntries = 1

	// Add memory entry
	// Note: fbMem points to the dedicated framebuffer region at a fixed physical address
	// (virtioGPUFramebufferAddr = 0x41000000), so we use that directly instead of
	// calling VirtqueueGetPhysicalAddr which expects a kernel virtual address.
	memEntryPtr := virtio.CastToPointer[VirtIOGPUMemEntry](virtio.PointerToUintptr(attachCmdBuf) + unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}))
	memEntryPtr.Addr = uint64(virtioGPUFramebufferAddr)
	memEntryPtr.Len = fbSize

	var attachResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(attachCmdBuf, attachCmdSize, unsafe.Pointer(&attachResp), uint32(unsafe.Sizeof(attachResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: ATTACH_BACKING failed (0x%04x)\n", respType)
		return false
	}

	// Step 3: Set scanout
	var scanoutCmd VirtIOGPUSetScanout
	scanoutCmd.Hdr.Type = VIRTIO_GPU_CMD_SET_SCANOUT
	scanoutCmd.Rect.Width = width
	scanoutCmd.Rect.Height = height
	scanoutCmd.ScanoutID = 0
	scanoutCmd.ResourceID = virtioGPUDevice.ResourceID

	var scanoutResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(unsafe.Pointer(&scanoutCmd), uint32(unsafe.Sizeof(scanoutCmd)), unsafe.Pointer(&scanoutResp), uint32(unsafe.Sizeof(scanoutResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: SET_SCANOUT failed (0x%04x)\n", respType)
		return false
	}

	console.KPrintf("[VirtIO GPU] Framebuffer ready (%dx%d @ 32bpp)\n", width, height)
	return true
}

// virtioGPUTransferToHost transfers framebuffer data to host (updates display)
//
//go:nosplit
func virtioGPUTransferToHost(x, y, width, height uint32) {
	// Flush framebuffer cache before DMA transfer to ensure GPU sees latest data
	pitch := virtioGPUDevice.Pitch
	fbBase := uintptr(virtioGPUDevice.Framebuffer)
	startOffset := uintptr(y)*uintptr(pitch) + uintptr(x)*4
	regionSize := uintptr(height) * uintptr(pitch)
	asm.CleanDCacheRange(fbBase+startOffset, regionSize)
	asm.DmaWmb()

	var transferCmd VirtIOGPUTransferToHost2D
	transferCmd.Hdr.Type = VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D
	transferCmd.Hdr.Flags = 0
	transferCmd.Hdr.FenceID = 0
	transferCmd.Hdr.CtxID = 0
	transferCmd.Hdr.Padding = 0
	transferCmd.Rect.X = x
	transferCmd.Rect.Y = y
	transferCmd.Rect.Width = width
	transferCmd.Rect.Height = height
	// Calculate offset: y * pitch + x * bytes_per_pixel
	transferCmd.Offset = uint64(y)*uint64(pitch) + uint64(x)*4
	transferCmd.ResourceID = virtioGPUDevice.ResourceID
	transferCmd.Padding = 0

	var transferResp VirtIOGPUCtrlHdr
	respType := virtioGPUSendCommand(unsafe.Pointer(&transferCmd), uint32(unsafe.Sizeof(transferCmd)), unsafe.Pointer(&transferResp), uint32(unsafe.Sizeof(transferResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		// Silently fail - don't spam UART
		return
	}
}

// virtioGPUFlush flushes a region of the framebuffer to the display
// This must be called after virtioGPUTransferToHost to make changes visible
//
//go:nosplit
func virtioGPUFlush(x, y, width, height uint32) {
	var flushCmd VirtIOGPUResourceFlush
	flushCmd.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_FLUSH
	flushCmd.Hdr.Flags = 0
	flushCmd.Hdr.FenceID = 0
	flushCmd.Hdr.CtxID = 0
	flushCmd.Hdr.Padding = 0
	flushCmd.Rect.X = x
	flushCmd.Rect.Y = y
	flushCmd.Rect.Width = width
	flushCmd.Rect.Height = height
	flushCmd.ResourceID = virtioGPUDevice.ResourceID
	flushCmd.Padding = 0

	var flushResp VirtIOGPUCtrlHdr
	respType := virtioGPUSendCommand(unsafe.Pointer(&flushCmd), uint32(unsafe.Sizeof(flushCmd)), unsafe.Pointer(&flushResp), uint32(unsafe.Sizeof(flushResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] FLUSH failed (0x%04x)\n", respType)
		return
	}
}
