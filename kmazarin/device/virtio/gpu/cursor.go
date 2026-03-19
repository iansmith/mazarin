package gpu

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/kmem"
	"unsafe"
)

// Cursor image dimensions — QEMU's virtio-gpu always allocates a 64x64
// cursor via cursor_alloc(64,64). If the resource dimensions don't match,
// virtio_gpu_update_cursor_data silently returns without copying pixel data.
const (
	CursorWidth  = 64
	CursorHeight = 64
	CursorHotX   = 0
	CursorHotY   = 0
)

// cursorResourceID is the resource ID used for the hardware cursor.
const cursorResourceID = 2

// cursorImageBGRA holds the 64x64 BGRA cursor image (16384 bytes).
// Generated from cursorBitmap at init time.
var cursorImageBGRA [CursorWidth * CursorHeight * 4]byte

// Standard arrow cursor bitmap.
// 0 = transparent, 1 = white outline, 2 = black fill.
var cursorBitmap = [CursorHeight][CursorWidth]byte{
	{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 2, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 2, 1, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 2, 1, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 1, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{1, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
}

// generateCursorImage converts cursorBitmap to BGRA pixel data.
func generateCursorImage() {
	for y := 0; y < CursorHeight; y++ {
		for x := 0; x < CursorWidth; x++ {
			off := (y*CursorWidth + x) * 4
			switch cursorBitmap[y][x] {
			case 0: // transparent
				cursorImageBGRA[off+0] = 0   // B
				cursorImageBGRA[off+1] = 0   // G
				cursorImageBGRA[off+2] = 0   // R
				cursorImageBGRA[off+3] = 0   // A
			case 1: // white outline
				cursorImageBGRA[off+0] = 255 // B
				cursorImageBGRA[off+1] = 255 // G
				cursorImageBGRA[off+2] = 255 // R
				cursorImageBGRA[off+3] = 255 // A
			case 2: // black fill
				cursorImageBGRA[off+0] = 0   // B
				cursorImageBGRA[off+1] = 0   // G
				cursorImageBGRA[off+2] = 0   // R
				cursorImageBGRA[off+3] = 255 // A
			}
		}
	}
}

// Static backing store command buffer for cursor resource.
var cursorAttachCmdBuf [unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{})]byte

// InitCursor creates the cursor resource, uploads the cursor image, and
// sends the initial UPDATE_CURSOR command. Must be called after GPU init.
func InitCursor() bool {
	generateCursorImage()

	// Step 1: Create 2D resource for cursor (32x32 BGRA with alpha)
	var createCmd VirtIOGPUResourceCreate2D
	createCmd.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_CREATE_2D
	createCmd.ResourceID = cursorResourceID
	createCmd.Format = VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM
	createCmd.Width = CursorWidth
	createCmd.Height = CursorHeight

	var createResp VirtIOGPUCtrlHdr
	respType := virtioGPUSendCommand(
		unsafe.Pointer(&createCmd), uint32(unsafe.Sizeof(createCmd)),
		unsafe.Pointer(&createResp), uint32(unsafe.Sizeof(createResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: cursor CREATE_2D failed (0x%04x)\n", respType)
		return false
	}

	// Step 2: Attach backing store (the cursorImageBGRA array)
	// Need physical address of cursor image — use page table walk.
	cursorImagePA := virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&cursorImageBGRA[0]))

	attachCmdSize := uint32(unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}) + unsafe.Sizeof(VirtIOGPUMemEntry{}))
	attachBuf := unsafe.Pointer(&cursorAttachCmdBuf[0])

	cmdPtr := virtio.CastToPointer[VirtIOGPUResourceAttachBacking](virtio.PointerToUintptr(attachBuf))
	cmdPtr.Hdr.Type = VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING
	cmdPtr.ResourceID = cursorResourceID
	cmdPtr.NrEntries = 1

	memEntryPtr := virtio.CastToPointer[VirtIOGPUMemEntry](
		virtio.PointerToUintptr(attachBuf) + unsafe.Sizeof(VirtIOGPUResourceAttachBacking{}))
	memEntryPtr.Addr = cursorImagePA
	memEntryPtr.Len = CursorWidth * CursorHeight * 4

	var attachResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(attachBuf, attachCmdSize,
		unsafe.Pointer(&attachResp), uint32(unsafe.Sizeof(attachResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: cursor ATTACH_BACKING failed (0x%04x)\n", respType)
		return false
	}

	// Step 3: Transfer cursor image to host
	var transferCmd VirtIOGPUTransferToHost2D
	transferCmd.Hdr.Type = VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D
	transferCmd.Rect.Width = CursorWidth
	transferCmd.Rect.Height = CursorHeight
	transferCmd.ResourceID = cursorResourceID

	var transferResp VirtIOGPUCtrlHdr
	respType = virtioGPUSendCommand(
		unsafe.Pointer(&transferCmd), uint32(unsafe.Sizeof(transferCmd)),
		unsafe.Pointer(&transferResp), uint32(unsafe.Sizeof(transferResp)))
	if respType != VIRTIO_GPU_RESP_OK_NODATA {
		console.KPrintf("[VirtIO GPU] ERROR: cursor TRANSFER failed (0x%04x)\n", respType)
		return false
	}

	// Step 4: Send UPDATE_CURSOR via cursorq (NOT controlq — QEMU only
	// processes cursor commands on the dedicated cursor queue).
	var updateCmd VirtIOGPUUpdateCursor
	updateCmd.Hdr.Type = VIRTIO_GPU_CMD_UPDATE_CURSOR
	updateCmd.Pos.ScanoutID = 0
	updateCmd.Pos.X = DisplayWidth / 2
	updateCmd.Pos.Y = DisplayHeight / 2
	updateCmd.ResourceID = cursorResourceID
	updateCmd.HotX = CursorHotX
	updateCmd.HotY = CursorHotY

	if !cursorqSendCommand(unsafe.Pointer(&updateCmd), uint32(unsafe.Sizeof(updateCmd))) {
		console.KPrintln("[VirtIO GPU] ERROR: UPDATE_CURSOR failed via cursorq")
		return false
	}

	console.KPrintln("[VirtIO GPU] Hardware cursor initialized")
	return true
}

// cursorqSendCommand sends a command via the cursor queue (queue 1) and polls
// for completion. QEMU's cursor handler does NOT write a response — it just
// pushes the descriptor back to the used ring with length 0.
func cursorqSendCommand(cmdBuf unsafe.Pointer, cmdSize uint32) bool {
	vq := &virtioGPUDevice.CursorQueue

	// Allocate a single descriptor (no response chain needed)
	cmdPhys := virtio.VirtqueueGetPhysicalAddr(cmdBuf)
	cmdDescIdx := virtio.VirtqueueAddDesc(vq, cmdPhys, cmdSize, 0, 0xFFFF)
	if cmdDescIdx == 0xFFFF {
		return false
	}

	// Add to available ring
	virtio.VirtqueueAddToAvailable(vq, cmdDescIdx)

	// Cache maintenance
	descTableSize := uintptr(vq.QueueSize) * unsafe.Sizeof(virtio.VirtQDesc{})
	descTableAddr := virtio.PointerToUintptr(vq.DescTable)
	asm.CleanDCacheRange(descTableAddr, descTableSize)
	availSize := uintptr(4 + vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(virtio.PointerToUintptr(unsafe.Pointer(vq.Available)), availSize)
	asm.DmaWmb()

	// Notify device via cursor queue notify offset
	queueNotifyAddr := virtioGPUDevice.NotifyBase +
		uintptr(virtioGPUDevice.CursorQueueNotifyOff)*uintptr(virtioGPUDevice.NotifyConfig.NotifyOffMultiplier)
	virtio.VirtqueueNotify(vq, queueNotifyAddr, 1)

	// Poll for completion
	maxWait := 1000000
	waited := 0
	for !virtio.VirtqueueHasUsed(vq) && waited < maxWait {
		for delay := 0; delay < 100; delay++ {
		}
		waited++
	}
	if waited >= maxWait {
		return false
	}

	asm.DmaRmb()
	usedDescIdx, _ := virtio.VirtqueueGetUsed(vq)
	if usedDescIdx == 0xFFFF {
		return false
	}
	virtio.VirtqueueFreeDescChain(vq, uint16(usedDescIdx))
	return true
}

// ---- Top-Half Cursor Move (nosplit, called from IRQ context) ----

// cursorq top-half state — all VAs point to DMA-mapped (Device-nGnRnE) memory,
// so MMIO reads/writes bypass the cache and are immediately visible to the device.
var topHalfCursorCmdVA uintptr // VA of MOVE_CURSOR command on DMA page
var topHalfCursorCmdPA uint64  // PA of MOVE_CURSOR command
var topHalfCursorAvailVA uintptr
var topHalfCursorDescVA uintptr
var topHalfCursorNotifyAddr uintptr
var topHalfCursorNextAvailIdx uint16
var topHalfCursorQueueSize uint16
var topHalfCursorUsedVA uintptr
var topHalfCursorLastUsedIdx uint16
var topHalfCursorReady bool

// InitCursorTopHalf allocates a DMA page for the cursor command buffer
// and wires the cursorq for top-half MOVE_CURSOR submissions.
func InitCursorTopHalf() bool {
	vq := &virtioGPUDevice.CursorQueue
	topHalfCursorQueueSize = vq.QueueSize

	// Allocate a DMA page for cursor command buffer.
	// DMA pages are mapped Device-nGnRnE, so MMIO writes bypass cache.
	dmaPA, dmaVA := kmem.AllocDMAPageMapped()
	if dmaPA == 0 {
		console.KPrintln("[VirtIO GPU] ERROR: Failed to alloc cursor DMA page")
		return false
	}

	// Layout on DMA page:
	//   offset 0: VirtIOGPUUpdateCursor command (56 bytes)
	//   (no response buffer — QEMU cursor handler doesn't write one)
	topHalfCursorCmdVA = dmaVA
	topHalfCursorCmdPA = uint64(dmaPA)

	// Initialize the command buffer on the DMA page with MOVE_CURSOR template
	asm.MmioWrite32(topHalfCursorCmdVA+0, VIRTIO_GPU_CMD_MOVE_CURSOR) // hdr.type
	asm.MmioWrite32(topHalfCursorCmdVA+4, 0)   // hdr.flags
	asm.MmioWrite32(topHalfCursorCmdVA+8, 0)   // hdr.fence_id low
	asm.MmioWrite32(topHalfCursorCmdVA+12, 0)  // hdr.fence_id high
	asm.MmioWrite32(topHalfCursorCmdVA+16, 0)  // hdr.ctx_id
	asm.MmioWrite32(topHalfCursorCmdVA+20, 0)  // hdr.padding
	asm.MmioWrite32(topHalfCursorCmdVA+24, 0)  // pos.scanout_id
	asm.MmioWrite32(topHalfCursorCmdVA+28, DisplayWidth/2)  // pos.x
	asm.MmioWrite32(topHalfCursorCmdVA+32, DisplayHeight/2) // pos.y
	asm.MmioWrite32(topHalfCursorCmdVA+36, 0)  // pos.padding
	asm.MmioWrite32(topHalfCursorCmdVA+40, cursorResourceID) // resource_id
	asm.MmioWrite32(topHalfCursorCmdVA+44, CursorHotX) // hot_x
	asm.MmioWrite32(topHalfCursorCmdVA+48, CursorHotY) // hot_y
	asm.MmioWrite32(topHalfCursorCmdVA+52, 0)  // padding

	topHalfCursorDescVA = uintptr(vq.DescTable)
	topHalfCursorAvailVA = uintptr(unsafe.Pointer(vq.Available))
	topHalfCursorUsedVA = uintptr(unsafe.Pointer(vq.Used))
	topHalfCursorNotifyAddr = virtioGPUDevice.NotifyBase +
		uintptr(virtioGPUDevice.CursorQueueNotifyOff)*uintptr(virtioGPUDevice.NotifyConfig.NotifyOffMultiplier)
	// Start from the current queue indices — cursorqSendCommand already used
	// the queue for UPDATE_CURSOR during InitCursor(), so Available.Idx and
	// LastUsedIdx are nonzero. The device will only process entries when we
	// advance the available index past what it last saw.
	topHalfCursorNextAvailIdx = vq.Available.Idx
	topHalfCursorLastUsedIdx = vq.LastUsedIdx
	topHalfCursorReady = true

	console.KPrintf("[VirtIO GPU] Cursor top-half ready (DMA page PA=0x%x)\n", dmaPA)
	return true
}

// TopHalfMoveCursor updates the hardware cursor position from IRQ context.
// All operations are nosplit-safe MMIO writes to DMA-mapped memory.
//
//go:nosplit
//go:noinline
func TopHalfMoveCursor(x, y uint32) {
	if !topHalfCursorReady {
		return
	}

	// Drain any completed commands from the used ring
	usedIdx := asm.MmioRead16(topHalfCursorUsedVA + 2)
	for topHalfCursorLastUsedIdx != usedIdx {
		topHalfCursorLastUsedIdx++
	}

	// Write new position to the command buffer (DMA-mapped, no cache issues)
	asm.MmioWrite32(topHalfCursorCmdVA+28, x) // pos.x
	asm.MmioWrite32(topHalfCursorCmdVA+32, y) // pos.y

	// Set up descriptor 0: command buffer (read-only, single descriptor).
	// QEMU cursor handler doesn't write a response — no need for a chained
	// response descriptor.
	descVA := topHalfCursorDescVA
	asm.MmioWrite32(descVA+0, uint32(topHalfCursorCmdPA))     // addr low
	asm.MmioWrite32(descVA+4, uint32(topHalfCursorCmdPA>>32)) // addr high
	asm.MmioWrite32(descVA+8, 56)                              // len = sizeof(UpdateCursor)
	asm.MmioWrite16(descVA+12, 0)                              // flags: no chain
	asm.MmioWrite16(descVA+14, 0xFFFF)                         // no next

	// Add descriptor 0 to available ring
	availRingIdx := topHalfCursorNextAvailIdx % topHalfCursorQueueSize
	asm.MmioWrite16(topHalfCursorAvailVA+4+uintptr(availRingIdx)*2, 0)
	topHalfCursorNextAvailIdx++

	asm.Dsb()
	asm.MmioWrite16(topHalfCursorAvailVA+2, topHalfCursorNextAvailIdx)
	asm.Dsb()

	// Kick the cursor queue (queue index 1)
	asm.MmioWrite16(topHalfCursorNotifyAddr, 1)
	asm.Dsb()
}
