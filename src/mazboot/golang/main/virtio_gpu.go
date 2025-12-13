//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
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

// VirtIOGPUDevice holds VirtIO GPU device state
type VirtIOGPUDevice struct {
	Bus              uint8
	Slot             uint8
	Func             uint8
	CommonConfig     VirtIOCapabilityInfo // Common Config capability
	NotifyConfig     VirtIOCapabilityInfo // Notify Config capability
	ISRConfig        VirtIOCapabilityInfo // ISR Config capability
	DeviceConfig     VirtIOCapabilityInfo // Device Config capability
	CommonConfigBase uintptr              // MMIO base for common config
	NotifyBase       uintptr              // MMIO base for notify
	ControlQueue     VirtQueue            // Control queue for GPU commands
	ResourceID       uint32               // Current resource ID
	Framebuffer      unsafe.Pointer       // Framebuffer memory
	FramebufferSize  uint32               // Framebuffer size in bytes
}

var virtioGPUDevice VirtIOGPUDevice

// Static framebuffer allocation (1280x720x4 bytes = 3,686,400 bytes)
// Pre-allocated to avoid kmalloc() write barrier stack issues
var virtioGPUFramebuffer [1280 * 720 * 4]byte

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
func virtioPCISetupQueue(queueIndex uint16, vq *VirtQueue) bool {
	// Select queue
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, queueIndex)

	// Set queue size
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE, vq.QueueSize)

	// Get physical addresses
	descPhys := virtqueueGetPhysicalAddr(vq.DescTable)
	availPhys := virtqueueGetPhysicalAddr(unsafe.Pointer(vq.Available))
	usedPhys := virtqueueGetPhysicalAddr(unsafe.Pointer(vq.Used))

	// Set descriptor table address (64-bit, split into low/high)
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW, uint32(descPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH, uint32(descPhys>>32))

	// Set available ring address
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW, uint32(availPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH, uint32(availPhys>>32))

	// Set used ring address
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW, uint32(usedPhys))
	virtioPCIWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH, uint32(usedPhys>>32))

	// Enable queue
	virtioPCIWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE, 1)

	uartPuts("VirtIO: Queue ")
	uartPutUint32(uint32(queueIndex))
	uartPuts(" enabled\r\n")

	return true
}

// findVirtIOGPU finds the VirtIO GPU PCI device
// Returns true if found, false otherwise
//
//go:nosplit
func findVirtIOGPU() bool {
	uartPuts("VirtIO GPU: Scanning for device...\r\n")

	// Scan PCI bus
	for bus := uint8(0); bus < 1; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			for funcNum := uint8(0); funcNum < 8; funcNum++ {
				// Read vendor/device ID
				fullReg := pciConfigRead32(bus, slot, funcNum, PCI_VENDOR_ID)
				vendorID := fullReg & 0xFFFF
				deviceID := (fullReg >> 16) & 0xFFFF

				// Check if device exists
				if vendorID == 0xFFFF || vendorID == 0 {
					continue
				}

				// Check if this is VirtIO GPU
				if vendorID == VIRTIO_VENDOR_ID && deviceID == VIRTIO_GPU_DEVICE_ID {
					uartPuts("VirtIO GPU: Found device!\r\n")
					uartPuts("  Bus: 0x")
					printHex32(uint32(bus))
					uartPuts(", Slot: 0x")
					printHex32(uint32(slot))
					uartPuts(", Func: 0x")
					printHex32(uint32(funcNum))
					uartPuts("\r\n")

					// Enable device
					cmd := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
					cmd |= 0x7 // Enable I/O, memory, bus master
					pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, cmd)

					// Find VirtIO capabilities
					var common, notify, isr, device VirtIOCapabilityInfo
					if !pciFindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &device) {
						uartPuts("VirtIO GPU: ERROR - Failed to find capabilities\r\n")
						return false
					}

					// Read BAR for common config
					barOffset := 0x10 + common.Bar*4 // BAR0 = 0x10, BAR1 = 0x14, etc.
					bar := pciConfigRead32(bus, slot, funcNum, uint8(barOffset))
					barBase := uintptr(bar & 0xFFFFFFF0) // Mask out type bits

					virtioGPUDevice.Bus = bus
					virtioGPUDevice.Slot = slot
					virtioGPUDevice.Func = funcNum
					virtioGPUDevice.CommonConfig = common
					virtioGPUDevice.NotifyConfig = notify
					virtioGPUDevice.ISRConfig = isr
					virtioGPUDevice.DeviceConfig = device
					virtioGPUDevice.CommonConfigBase = barBase + uintptr(common.OffsetInBar)

					// Calculate notify base
					notifyBarOffset := 0x10 + notify.Bar*4
					notifyBar := pciConfigRead32(bus, slot, funcNum, uint8(notifyBarOffset))
					notifyBarBase := uintptr(notifyBar & 0xFFFFFFF0)
					virtioGPUDevice.NotifyBase = notifyBarBase + uintptr(notify.OffsetInBar)

					uartPuts("VirtIO GPU: Common config at 0x")
					for shift := 60; shift >= 0; shift -= 4 {
						digit := (uint64(virtioGPUDevice.CommonConfigBase) >> shift) & 0xF
						if digit < 10 {
							uartPutc(byte('0' + digit))
						} else {
							uartPutc(byte('A' + digit - 10))
						}
					}
					uartPuts("\r\n")

					return true
				}
			}
		}
	}

	uartPuts("VirtIO GPU: Device not found\r\n")
	return false
}

// virtioGPUInit initializes the VirtIO GPU device
// Returns true on success, false on failure
//
//go:nosplit
func virtioGPUInit() bool {
	uartPuts("VirtIO GPU: Initializing...\r\n")

	// Step 1: Reset device
	virtioPCISetDeviceStatus(0)

	// Step 2: Acknowledge device
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE)

	// Step 3: Driver is present
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER)

	// Step 4: Feature negotiation (skip for now - accept device defaults)
	// In a full implementation, we'd read device features and negotiate

	// Step 5: Features OK
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK)

	// Step 6: Initialize control queue (queue 0)
	queueSize := uint16(256) // Power of 2
	if !virtqueueInit(&virtioGPUDevice.ControlQueue, queueSize) {
		uartPuts("VirtIO GPU: ERROR - Failed to initialize control queue\r\n")
		return false
	}

	// Step 7: Setup queue in device
	if !virtioPCISetupQueue(0, &virtioGPUDevice.ControlQueue) {
		uartPuts("VirtIO GPU: ERROR - Failed to setup control queue\r\n")
		return false
	}

	// Step 8: Driver OK
	virtioPCISetDeviceStatus(VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK | VIRTIO_STATUS_DRIVER_OK)

	// Allocate framebuffer memory here (before setup, to reduce stack pressure in framebufferInit)
	// This will be used later in virtioGPUSetupFramebuffer
	// For now, just initialize the resource ID
	virtioGPUDevice.ResourceID = 1

	uartPuts("VirtIO GPU: Initialized successfully\r\n")
	return true
}

// virtioGPUSendCommand sends a GPU command via the control queue
// Returns response type, or 0xFFFF on error
//
//go:nosplit
func virtioGPUSendCommand(cmdBuf unsafe.Pointer, cmdSize uint32, respBuf unsafe.Pointer, respSize uint32) uint32 {
	vq := &virtioGPUDevice.ControlQueue

	// Allocate descriptors for command and response
	cmdPhys := virtqueueGetPhysicalAddr(cmdBuf)
	cmdDescIdx := virtqueueAddDesc(vq, cmdPhys, cmdSize, 0, 0xFFFF)
	if cmdDescIdx == 0xFFFF {
		uartPuts("VirtIO GPU: ERROR - Failed to add command descriptor\r\n")
		return 0xFFFF
	}

	respPhys := virtqueueGetPhysicalAddr(respBuf)
	respDescIdx := virtqueueAddDesc(vq, respPhys, respSize, VIRTQ_DESC_F_WRITE, 0xFFFF)
	if respDescIdx == 0xFFFF {
		uartPuts("VirtIO GPU: ERROR - Failed to add response descriptor\r\n")
		return 0xFFFF
	}

	// Link descriptors
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	cmdDescPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(cmdDescIdx)*descSize)
	cmdDescPtr.Flags |= VIRTQ_DESC_F_NEXT
	cmdDescPtr.Next = respDescIdx

	// Add to available ring
	virtqueueAddToAvailable(vq, cmdDescIdx)

	// Notify device
	queueNotifyOffset := virtioGPUDevice.NotifyBase + uintptr(vq.QueueSize)*2 // notify_off_multiplier * queue_index
	virtqueueNotify(vq, queueNotifyOffset)

	// Poll for response (simplified - in production, use interrupts or better polling)
	maxWait := 1000000
	waited := 0
	for !virtqueueHasUsed(vq) && waited < maxWait {
		for delay := 0; delay < 100; delay++ {
		}
		waited++
	}

	if waited >= maxWait {
		uartPuts("VirtIO GPU: ERROR - Command timeout\r\n")
		return 0xFFFF
	}

	// Get response
	usedDescIdx, _ := virtqueueGetUsed(vq)
	if usedDescIdx == 0xFFFF {
		uartPuts("VirtIO GPU: ERROR - Failed to get used descriptor\r\n")
		return 0xFFFF
	}

	// Free descriptor chain
	virtqueueFreeDescChain(vq, uint16(usedDescIdx))

	// Read response type from response buffer
	respHdr := (*VirtIOGPUCtrlHdr)(respBuf)
	return respHdr.Type
}

// virtioGPUSetupFramebuffer sets up the framebuffer using VirtIO GPU
// Allocates framebuffer memory internally
// Returns true on success, false on failure
//
//go:nosplit
func virtioGPUSetupFramebuffer(width, height uint32) bool {
	uartPuts("VirtIO GPU: Setting up framebuffer ")
	uartPutUint32(width)
	uartPuts("x")
	uartPutUint32(height)
	uartPuts("\r\n")

	// Use pre-allocated static framebuffer (avoids kmalloc write barrier issues)
	fbSize := width * height * 4 // 4 bytes per pixel (BGRA8888)
	if fbSize > uint32(len(virtioGPUFramebuffer)) {
		uartPuts("VirtIO GPU: ERROR - Framebuffer too large for static allocation\r\n")
		return false
	}

	// Zero framebuffer
	fbMem := unsafe.Pointer(&virtioGPUFramebuffer[0])
	asm.Bzero(fbMem, fbSize)

	virtioGPUDevice.Framebuffer = fbMem
	virtioGPUDevice.FramebufferSize = fbSize

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
		uartPuts("VirtIO GPU: ERROR - Create resource failed, response=0x")
		printHex32(respType)
		uartPuts("\r\n")
		return false
	}

	uartPuts("VirtIO GPU: Resource created\r\n")

	// Step 2: Attach backing store
	// Use static buffer (avoids kmalloc write barrier issues)
	attachCmdSize := uint32(unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{}))
	attachCmdBuf := unsafe.Pointer(&virtioGPUAttachCmdBuf[0])

	// Set up command structure
	cmdPtr := castToPointer[VirtIOGPUResourceAttachBacking](pointerToUintptr(attachCmdBuf))
	cmdPtr.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING
	cmdPtr.ResourceID = virtioGPUDevice.ResourceID
	cmdPtr.NrEntries = 1

	// Add memory entry
	memEntryPtr := castToPointer[VirtIOGPUMemEntry](pointerToUintptr(attachCmdBuf) + unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}))
	memEntryPtr.Addr = virtqueueGetPhysicalAddr(fbMem)
	memEntryPtr.Len = fbSize

	var attachResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(attachCmdBuf, attachCmdSize, unsafe.Pointer(&attachResp), uint32(unsafe.Sizeof(attachResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		uartPuts("VirtIO GPU: ERROR - Attach backing failed, response=0x")
		printHex32(respType)
		uartPuts("\r\n")
		return false
	}

	uartPuts("VirtIO GPU: Backing store attached\r\n")

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
		uartPuts("VirtIO GPU: ERROR - Set scanout failed, response=0x")
		printHex32(respType)
		uartPuts("\r\n")
		return false
	}

	uartPuts("VirtIO GPU: Scanout set\r\n")

	uartPuts("VirtIO GPU: Framebuffer setup complete\r\n")
	return true
}

// virtioGPUTransferToHost transfers framebuffer data to host (updates display)
//
//go:nosplit
func virtioGPUTransferToHost(x, y, width, height uint32) {
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
	// Pitch = width * 4 (BGRA8888 = 4 bytes per pixel)
	// We know the width from setup (1280 pixels = 5120 bytes per row)
	pitch := uint32(1280 * 4) // 1280 pixels * 4 bytes per pixel
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
