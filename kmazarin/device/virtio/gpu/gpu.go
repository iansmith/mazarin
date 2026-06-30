
package gpu

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
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

	// Cursor commands (use cursorq, not controlq)
	VIRTIO_GPU_CMD_UPDATE_CURSOR = 0x0300
	VIRTIO_GPU_CMD_MOVE_CURSOR   = 0x0301
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

// VirtIO GPU Pixel Formats (values from VirtIO spec)
const (
	VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM = 1
	VIRTIO_GPU_FORMAT_B8G8R8X8_UNORM = 2
	VIRTIO_GPU_FORMAT_R8G8B8A8_UNORM = 67
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

// VirtIOGPUCursorPos describes cursor position
type VirtIOGPUCursorPos struct {
	ScanoutID uint32
	X         uint32
	Y         uint32
	Padding   uint32
}

// VirtIOGPUUpdateCursor is the command for UPDATE_CURSOR and MOVE_CURSOR
type VirtIOGPUUpdateCursor struct {
	Hdr        VirtIOGPUCtrlHdr
	Pos        VirtIOGPUCursorPos
	ResourceID uint32 // Cursor image resource ID
	HotX       uint32 // Hot spot X offset
	HotY       uint32 // Hot spot Y offset
	Padding    uint32
}

// VirtIOGPUDevice holds VirtIO GPU device state.
// Embeds virtio.PCIDevice for shared PCI transport state.
type VirtIOGPUDevice struct {
	virtio.PCIDevice // Shared PCI transport (promoted: Bus, Slot, Func, NotifyBase, etc.)

	ControlQueue          virtio.VirtQueue // Control queue for GPU commands
	ControlQueueNotifyOff uint16           // Notify offset for control queue
	CursorQueue           virtio.VirtQueue // Cursor queue (queue 1) for cursor updates
	CursorQueueNotifyOff  uint16           // Notify offset for cursor queue
	ResourceID            uint32           // Current resource ID
	Framebuffer           unsafe.Pointer   // Framebuffer memory
	FramebufferSize       uint32           // Framebuffer size in bytes
	Width                 uint32           // Display width in pixels
	Height                uint32           // Display height in pixels (visible area)
	ResourceHeight        uint32           // Total resource height (may be > Height for scrolling)
	Pitch                 uint32           // Bytes per row
}

var virtioGPUDevice VirtIOGPUDevice

// Framebuffer physical address. Dynamically allocated during GPU init, sized to
// the detected display resolution (see virtioGPUSetupFramebuffer).
var virtioGPUFramebufferAddr uintptr

// framebufferMaxBytes caps the framebuffer allocation. The single source of truth
// is the kernel TOML (framebuffer_max_mb), applied via SetFramebufferMaxBytes
// before Init(); this is just the built-in default until then.
var framebufferMaxBytes uint32 = constants.DefaultFramebufferMaxMB * 1024 * 1024

// SetFramebufferMaxBytes overrides the framebuffer size cap from kernel config.
// Must be called before Init(). A value of 0 is ignored (keeps the default).
func SetFramebufferMaxBytes(n uint32) {
	if n > 0 {
		framebufferMaxBytes = n
	}
}

// Static buffer for attach backing command (small, avoids kmalloc)
var virtioGPUAttachCmdBuf [unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{})]byte

// findVirtIOGPU finds the VirtIO GPU PCI device.
// On success, populates the embedded PCIDevice with BAR mappings.
//
//go:nosplit
func findVirtIOGPU() bool {
	// GPU uses PCI_MMIO_BASE directly (block uses +0x200000 to avoid collision)
	const gpuMMIOBase = pci.PCI_MMIO_BASE

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

				if vendorID == pci.VIRTIO_VENDOR_ID && deviceID == pci.VIRTIO_GPU_DEVICE_ID {
					if !virtioGPUDevice.FindAndMapBARs(bus, slot, funcNum, gpuMMIOBase) {
						klog.Errf("[VirtIO GPU] ERROR: Failed to find/map BARs\n")
						return false
					}
					return true
				}
			}
		}
	}

	return false
}

// virtioGPUInit initializes the VirtIO GPU device.
// Uses shared PCIDevice methods for handshake and queue setup.
// Returns true on success, false on failure.
//
//go:nosplit
func virtioGPUInit() bool {
	dev := &virtioGPUDevice

	// Feature negotiation: accept VIRTIO_F_VERSION_1 only
	if !dev.Handshake(0, virtio.FeatureVersion1) {
		klog.Errf("[VirtIO GPU] ERROR: Device rejected features\n")
		return false
	}

	// Initialize control queue (heap-allocated, uses VirtqueueInit)
	queueSize := uint16(64)
	if !virtio.VirtqueueInit(&dev.ControlQueue, queueSize) {
		klog.Errf("[VirtIO GPU] ERROR: Failed to init control queue\n")
		return false
	}

	// Enable control queue on the device (EnableQueue handles heap PAs)
	notifyOff, ok := dev.EnableQueue(0, &dev.ControlQueue, virtio.MSIXNoVector)
	if !ok {
		klog.Errf("[VirtIO GPU] ERROR: Failed to setup control queue\n")
		return false
	}
	dev.ControlQueueNotifyOff = notifyOff

	// Initialize cursor queue (DMA-page, queue index 1) so the top-half
	// can submit MOVE_CURSOR commands directly via MMIO without cache issues.
	cursorQueueSize := uint16(16)
	cursorDmaPA, cursorDmaVA := kmem.AllocDMAPageMapped()
	if cursorDmaPA == 0 {
		klog.Errf("[VirtIO GPU] ERROR: Failed to alloc cursor queue DMA page\n")
		return false
	}
	notifyOff, ok = dev.SetupQueue(1, &dev.CursorQueue, cursorQueueSize, cursorDmaPA, cursorDmaVA, 0, virtio.MSIXNoVector)
	if !ok {
		klog.Errf("[VirtIO GPU] ERROR: Failed to setup cursor queue\n")
		return false
	}
	dev.CursorQueueNotifyOff = notifyOff

	// Complete handshake
	dev.SetDriverOK()

	if dev.CheckFailed() {
		klog.Errf("[VirtIO GPU] ERROR: Device failed\n")
		return false
	}

	dev.ResourceID = 1
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
		return 0xFFFF
	}

	respPhys := virtio.VirtqueueGetPhysicalAddr(respBuf)
	respDescIdx := virtio.VirtqueueAddDesc(vq, respPhys, respSize, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if respDescIdx == 0xFFFF {
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
		return 0xFFFF
	}

	// DMA read barrier - ensure device writes are visible
	asm.DmaRmb()

	// Get response
	usedDescIdx, _ := virtio.VirtqueueGetUsed(vq)
	if usedDescIdx == 0xFFFF {
		klog.Errf("[VirtIO GPU] ERROR: Failed to get used descriptor\n")
		return 0xFFFF
	}

	// Free descriptor chain
	virtio.VirtqueueFreeDescChain(vq, uint16(usedDescIdx))

	// Invalidate D-cache for response buffer before reading DMA-written data
	asm.InvalidateDCacheRange(uintptr(respBuf), uintptr(respSize))
	asm.DmaRmb()

	// Read response type from response buffer
	respHdr := (*VirtIOGPUCtrlHdr)(respBuf)
	return respHdr.Type
}

// virtioGPUGetDisplayInfo queries the GPU for display information.
// Returns (width, height, ok). On success, returns the host's preferred
// display dimensions from the first enabled scanout. On failure, returns
// (0, 0, false).
func virtioGPUGetDisplayInfo() (uint32, uint32, bool) {
	// Prepare GET_DISPLAY_INFO command
	var cmd VirtIOGPUCtrlHdr
	cmd.Type = VIRTIO_GPU_CMD_GET_DISPLAY_INFO
	cmd.Flags = 0
	cmd.FenceID = 0
	cmd.CtxID = 0
	cmd.Padding = 0

	// Response: VirtIOGPUCtrlHdr (24 bytes) + up to 16 display_one entries.
	// Each display_one: VirtIOGPURect (16 bytes) + enabled (4 bytes) + flags (4 bytes) = 24 bytes.
	// Total: 24 + 16*24 = 408 bytes.
	var resp [408]byte

	respType := virtioGPUSendCommand(
		unsafe.Pointer(&cmd),
		uint32(unsafe.Sizeof(cmd)),
		unsafe.Pointer(&resp[0]),
		uint32(len(resp)))

	if respType != VIRTIO_GPU_RESP_OK_DISPLAY_INFO {
		return 0, 0, false
	}

	// Parse first enabled scanout. Each display_one entry starts at offset 24 + i*24.
	for i := 0; i < 16; i++ {
		off := 24 + i*24
		// VirtIOGPURect: x(4), y(4), width(4), height(4)
		w := *(*uint32)(unsafe.Pointer(&resp[off+8]))
		h := *(*uint32)(unsafe.Pointer(&resp[off+12]))
		enabled := *(*uint32)(unsafe.Pointer(&resp[off+16]))
		if enabled != 0 && w > 0 && h > 0 {
			klog.Logf("[VirtIO GPU] Display info: scanout %d = %dx%d\n", i, w, h)
			return w, h, true
		}
	}

	return 0, 0, false
}

// virtioGPUSetupFramebuffer sets up the framebuffer using VirtIO GPU.
// displayWidth/displayHeight: the visible display dimensions
// resourceHeight: total height of the GPU resource (>= displayHeight for scrolling)
// Returns true on success, false on failure
//
func virtioGPUSetupFramebuffer(displayWidth, displayHeight, resourceHeight uint32) bool {
	// Resource can be taller than display to enable hardware scrolling. Compute in
	// 64-bit so a buggy hypervisor reporting absurd dimensions cannot wrap uint32
	// and slip past the cap; framebufferMaxBytes is uint32, so once the value is
	// within the cap it fits uint32.
	fbSize64 := uint64(displayWidth) * uint64(resourceHeight) * 4 // 4 bytes per pixel (BGRA8888)

	if fbSize64 > uint64(framebufferMaxBytes) {
		// Single source of truth for the cap is the kernel TOML framebuffer_max_mb
		// (see SetFramebufferMaxBytes). Panic rather than silently over-allocate.
		klog.Fatalf("FB too big",
			"[VirtIO GPU] framebuffer %dx%d = %d bytes (incl. 2x scroll height) exceeds framebuffer_max_mb cap of %d bytes\n",
			displayWidth, resourceHeight, fbSize64, framebufferMaxBytes)
	}
	fbSize := uint32(fbSize64)

	// Allocate framebuffer from the physical page pool.
	// Must be contiguous for DMA (VirtIO ATTACH_BACKING needs a single PA range).
	fbPages := (uintptr(fbSize) + kmem.PageSize - 1) / kmem.PageSize
	virtioGPUFramebufferAddr = kmem.AllocContiguousPages(fbPages, kmem.PageFramebuffer, 0)
	if virtioGPUFramebufferAddr == 0 {
		klog.Errf("[VirtIO GPU] ERROR: Failed to allocate framebuffer pages\n")
		return false
	}
	// Framebuffer allocated

	// Zero framebuffer (use linear map VA for CPU access)
	fbVA := virtioGPUFramebufferAddr + constants.KernelVAOffset
	fbMem := unsafe.Pointer(fbVA)
	virtio.Bzero4K(fbMem, fbSize)

	virtioGPUDevice.Framebuffer = fbMem
	virtioGPUDevice.FramebufferSize = fbSize
	virtioGPUDevice.Width = displayWidth
	virtioGPUDevice.Height = displayHeight
	virtioGPUDevice.ResourceHeight = resourceHeight
	virtioGPUDevice.Pitch = displayWidth * 4

	// Step 1: Create 2D resource (may be taller than display for scrolling)
	var createCmd VirtIOGPUResourceCreate2D
	createCmd.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_CREATE_2D
	createCmd.ResourceID = virtioGPUDevice.ResourceID
	createCmd.Format = VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM
	createCmd.Width = displayWidth
	createCmd.Height = resourceHeight

	var createResp VirtIOGPUCtrlHdr

	respType := virtioGPUSendCommand(unsafe.Pointer(&createCmd), uint32(unsafe.Sizeof(createCmd)), unsafe.Pointer(&createResp), uint32(unsafe.Sizeof(createResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		klog.Errf("[VirtIO GPU] ERROR: CREATE_2D failed (0x%04x)\n", respType)
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

	// Add memory entry — use the dynamically allocated physical address directly.
	memEntryPtr := virtio.CastToPointer[VirtIOGPUMemEntry](virtio.PointerToUintptr(attachCmdBuf) + unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}))
	memEntryPtr.Addr = uint64(virtioGPUFramebufferAddr)
	memEntryPtr.Len = fbSize

	var attachResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(attachCmdBuf, attachCmdSize, unsafe.Pointer(&attachResp), uint32(unsafe.Sizeof(attachResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		klog.Errf("[VirtIO GPU] ERROR: ATTACH_BACKING failed (0x%04x)\n", respType)
		return false
	}

	// Step 3: Set scanout (display the top displayHeight pixels of the resource)
	var scanoutCmd VirtIOGPUSetScanout
	scanoutCmd.Hdr.Type = VIRTIO_GPU_CMD_SET_SCANOUT
	scanoutCmd.Rect.Width = displayWidth
	scanoutCmd.Rect.Height = displayHeight
	scanoutCmd.ScanoutID = 0
	scanoutCmd.ResourceID = virtioGPUDevice.ResourceID

	var scanoutResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(unsafe.Pointer(&scanoutCmd), uint32(unsafe.Sizeof(scanoutCmd)), unsafe.Pointer(&scanoutResp), uint32(unsafe.Sizeof(scanoutResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		klog.Errf("[VirtIO GPU] ERROR: SET_SCANOUT failed (0x%04x)\n", respType)
		return false
	}

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
		return
	}
}

// virtioGPUSetScanoutOffset changes the visible region of the framebuffer.
// This enables hardware-accelerated scrolling by changing which portion of a
// larger backing buffer is displayed, rather than copying pixels.
// yOffset: vertical offset in pixels from the top of the resource
//
//go:nosplit
func virtioGPUSetScanoutOffset(yOffset uint32) bool {
	var scanoutCmd VirtIOGPUSetScanout
	scanoutCmd.Hdr.Type = VIRTIO_GPU_CMD_SET_SCANOUT
	scanoutCmd.Rect.X = 0
	scanoutCmd.Rect.Y = yOffset
	scanoutCmd.Rect.Width = virtioGPUDevice.Width
	scanoutCmd.Rect.Height = virtioGPUDevice.Height
	scanoutCmd.ScanoutID = 0
	scanoutCmd.ResourceID = virtioGPUDevice.ResourceID

	var scanoutResp VirtIOGPUCtrlHdr
	respType := virtioGPUSendCommand(unsafe.Pointer(&scanoutCmd), uint32(unsafe.Sizeof(scanoutCmd)), unsafe.Pointer(&scanoutResp), uint32(unsafe.Sizeof(scanoutResp)))
	return respType == VIRTIO_GPU_RESP_OK_NODATA
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
		return
	}
}

// virtioGPUTransferAndFlush batches TRANSFER_TO_HOST_2D + RESOURCE_FLUSH
// into a single virtqueue submission. Both descriptor chains are added to the
// available ring before notifying the device, and we poll once for both
// completions. This eliminates one notify + poll round-trip compared to
// calling virtioGPUTransferToHost + virtioGPUFlush separately.
//
//go:nosplit
func virtioGPUTransferAndFlush(x, y, width, height uint32) {
	// Flush framebuffer cache before DMA transfer
	pitch := virtioGPUDevice.Pitch
	fbBase := uintptr(virtioGPUDevice.Framebuffer)
	startOffset := uintptr(y)*uintptr(pitch) + uintptr(x)*4
	regionSize := uintptr(height) * uintptr(pitch)
	asm.CleanDCacheRange(fbBase+startOffset, regionSize)
	asm.DmaWmb()

	// Build transfer command
	var transferCmd VirtIOGPUTransferToHost2D
	transferCmd.Hdr.Type = VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D
	transferCmd.Rect.X = x
	transferCmd.Rect.Y = y
	transferCmd.Rect.Width = width
	transferCmd.Rect.Height = height
	transferCmd.Offset = uint64(y)*uint64(pitch) + uint64(x)*4
	transferCmd.ResourceID = virtioGPUDevice.ResourceID

	// Build flush command
	var flushCmd VirtIOGPUResourceFlush
	flushCmd.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_FLUSH
	flushCmd.Rect.X = x
	flushCmd.Rect.Y = y
	flushCmd.Rect.Width = width
	flushCmd.Rect.Height = height
	flushCmd.ResourceID = virtioGPUDevice.ResourceID

	var transferResp VirtIOGPUCtrlHdr
	var flushResp VirtIOGPUCtrlHdr

	vq := &virtioGPUDevice.ControlQueue

	// --- Set up transfer descriptor chain (cmd → resp) ---
	tCmdPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&transferCmd))
	tCmdIdx := virtio.VirtqueueAddDesc(vq, tCmdPhys, uint32(unsafe.Sizeof(transferCmd)), 0, 0xFFFF)
	if tCmdIdx == 0xFFFF {
		return
	}
	tRespPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&transferResp))
	tRespIdx := virtio.VirtqueueAddDesc(vq, tRespPhys, uint32(unsafe.Sizeof(transferResp)), virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if tRespIdx == 0xFFFF {
		virtio.VirtqueueFreeDescChain(vq, tCmdIdx)
		return
	}
	// Link transfer cmd → resp
	descSize := unsafe.Sizeof(virtio.VirtQDesc{})
	tCmdPtr := virtio.CastToPointer[virtio.VirtQDesc](virtio.PointerToUintptr(vq.DescTable) + uintptr(tCmdIdx)*descSize)
	tCmdPtr.Flags |= virtio.VIRTQ_DESC_F_NEXT
	tCmdPtr.Next = tRespIdx

	// --- Set up flush descriptor chain (cmd → resp) ---
	fCmdPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&flushCmd))
	fCmdIdx := virtio.VirtqueueAddDesc(vq, fCmdPhys, uint32(unsafe.Sizeof(flushCmd)), 0, 0xFFFF)
	if fCmdIdx == 0xFFFF {
		virtio.VirtqueueFreeDescChain(vq, tCmdIdx)
		return
	}
	fRespPhys := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&flushResp))
	fRespIdx := virtio.VirtqueueAddDesc(vq, fRespPhys, uint32(unsafe.Sizeof(flushResp)), virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if fRespIdx == 0xFFFF {
		virtio.VirtqueueFreeDescChain(vq, tCmdIdx)
		virtio.VirtqueueFreeDescChain(vq, fCmdIdx)
		return
	}
	// Link flush cmd → resp
	fCmdPtr := virtio.CastToPointer[virtio.VirtQDesc](virtio.PointerToUintptr(vq.DescTable) + uintptr(fCmdIdx)*descSize)
	fCmdPtr.Flags |= virtio.VIRTQ_DESC_F_NEXT
	fCmdPtr.Next = fRespIdx

	// --- Add both chains to available ring, one notify, poll for both ---
	virtio.VirtqueueAddToAvailable(vq, tCmdIdx)
	virtio.VirtqueueAddToAvailable(vq, fCmdIdx)

	// Cache maintenance for descriptors and available ring
	descTableSize := uintptr(vq.QueueSize) * unsafe.Sizeof(virtio.VirtQDesc{})
	descTableAddr := virtio.PointerToUintptr(vq.DescTable)
	asm.CleanDCacheRange(descTableAddr, descTableSize)
	availSize := uintptr(4 + vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(virtio.PointerToUintptr(unsafe.Pointer(vq.Available)), availSize)
	asm.DmaWmb()

	// Single notify for both commands
	queueNotifyAddr := virtioGPUDevice.NotifyBase +
		uintptr(virtioGPUDevice.ControlQueueNotifyOff)*uintptr(virtioGPUDevice.NotifyConfig.NotifyOffMultiplier)
	virtio.VirtqueueNotify(vq, queueNotifyAddr, 0)

	// Poll until both completions arrive (2 used entries)
	maxWait := 1000000
	completions := 0
	waited := 0
	for completions < 2 && waited < maxWait {
		if virtio.VirtqueueHasUsed(vq) {
			asm.DmaRmb()
			usedIdx, _ := virtio.VirtqueueGetUsed(vq)
			if usedIdx != 0xFFFF {
				virtio.VirtqueueFreeDescChain(vq, uint16(usedIdx))
				completions++
			}
		}
		if completions < 2 {
			for delay := 0; delay < 100; delay++ {
			}
			waited++
		}
	}
	if waited >= maxWait {
		// timeout — silently drop
	}

	// Invalidate response buffers and check results
	asm.InvalidateDCacheRange(uintptr(unsafe.Pointer(&transferResp)), uintptr(unsafe.Sizeof(transferResp)))
	asm.InvalidateDCacheRange(uintptr(unsafe.Pointer(&flushResp)), uintptr(unsafe.Sizeof(flushResp)))
	asm.DmaRmb()
}
