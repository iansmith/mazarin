package block

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
)

// ReadBlock reads a single block at the given LBA
// buf must be at least BlockSize() bytes
//
//go:nosplit
func (d *VirtIOBlockDevice) ReadBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) < d.BlockSize() {
		klog.Errf("[VirtIO Block] ERROR: Buffer too small for read\n")
		return ErrBufferTooSmall
	}

	return d.doBlockIO(VIRTIO_BLK_T_IN, lba, buf)
}

// WriteBlock writes a single block at the given LBA
// buf must be exactly BlockSize() bytes
//
//go:nosplit
func (d *VirtIOBlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) != d.BlockSize() {
		klog.Errf("[VirtIO Block] ERROR: Buffer size must equal block size for write\n")
		return ErrInvalidSize
	}

	return d.doBlockIO(VIRTIO_BLK_T_OUT, lba, buf)
}

// MaxInFlight is the maximum number of concurrent PCI block I/O requests.
// Each in-flight request uses one sidecar slot (header+status) and one data
// buffer region in the DMA page. With 512-byte blocks: 8 × 512 = 4096 = 1 page.
const MaxInFlight = 8

// DMA layout constants.
//
// PCI Engine mode: sidecar slots hold header+status (Device-nGnRnE),
// DMA data page holds per-slot data buffers at offset slotIdx*blockSize.
// MMIO legacy mode: single DMA page holds header+status+data.
const (
	// PCI sidecar slot layout (32 bytes per slot)
	sidecarSlotSize     = 32
	sidecarReqOffset    = 0  // VirtIOBlockReq header: 16 bytes
	sidecarStatusOffset = 16 // VirtIOBlockStatus: 1 byte

	// MMIO legacy DMA page layout
	dmaReqOffset    = 0  // VirtIOBlockReq header: 16 bytes
	dmaStatusOffset = 16 // Status byte: 1 byte
	dmaBufOffset    = 32 // Data buffer: 512 bytes (aligned to 32)
)

// DoBlockIOSubmit sets up descriptors and submits a block I/O request.
// Returns the head descriptor index, or 0xFFFF on error.
// Does NOT notify the device — the caller must call Eng.Notify() after
// submitting one or more requests (allows batching multiple submits with
// a single notify).
//
// slotIdx selects which data buffer region in the DMA page to use (0..MaxInFlight-1).
// Each slot's data buffer is at DmaPagePA + slotIdx*BlockSizeBytes.
//
// extDataPA: if non-zero, use this PA as the data buffer instead of the kernel
// DMA page slot. Used for zero-copy DMA pool I/O where userspace pages are
// registered with pre-cached PAs. When extDataPA is set, buf is ignored for
// write operations (caller must have already written data to the userspace page).
//
// PCI mode uses Engine + SidecarPool. MMIO mode uses legacy Queue + DMA page.
//
// dataLen overrides the data descriptor length when non-zero. When 0,
// defaults to d.BlockSizeBytes (single-sector I/O). Used by SyscallBlockSubmit
// to issue multi-sector reads (e.g., 8 sectors = 4096 bytes).
//
//go:nosplit
func (d *VirtIOBlockDevice) DoBlockIOSubmit(requestType uint32, lba uint64, buf []byte, slotIdx uint8, extDataPA uintptr, dataLen uint32) (uint16, error) {
	// Default dataLen to single-sector size when not specified
	if dataLen == 0 {
		dataLen = d.BlockSizeBytes
	}

	if d.MMIOBase != 0 {
		return d.submitLegacy(requestType, lba, buf)
	}

	// --- PCI Engine path ---

	// Allocate sidecar slot for protocol header + status
	slot, ok := d.Sidecars.Alloc()
	if !ok {
		klog.Errf("[VirtIO Block] ERROR: No sidecar slots available\n")
		return 0xFFFF, ErrNoDescriptors
	}
	d.inFlightSidecars[slotIdx] = slot

	// Write request header into sidecar slot
	reqPtr := (*VirtIOBlockReq)(unsafe.Pointer(slot.VA + sidecarReqOffset))
	reqPtr.Type = requestType
	reqPtr.Sector = lba

	// Initialize status byte to 0xFF (sentinel)
	*(*VirtIOBlockStatus)(unsafe.Pointer(slot.VA + sidecarStatusOffset)) = 0xFF

	// Determine data buffer PA. If extDataPA is set (DMA pool zero-copy),
	// use it directly. Otherwise use the kernel DMA page at slotIdx offset.
	dataPA := extDataPA
	if dataPA == 0 {
		dataPA = d.DmaPagePA + uintptr(slotIdx)*uintptr(d.BlockSizeBytes)
		dataVA := d.DmaPageVA + uintptr(slotIdx)*uintptr(d.BlockSizeBytes)

		// For writes, copy caller's data into DMA data buffer before submitting
		if requestType == VIRTIO_BLK_T_OUT && len(buf) > 0 {
			copyBytes(unsafe.Pointer(dataVA), unsafe.Pointer(&buf[0]), uintptr(d.BlockSizeBytes))
		}
	}

	// Build 3-descriptor chain: req header → data buffer → status byte
	var chain virtio.DescChain
	chain.Count = 3
	chain.Descs[0] = virtio.DescSpec{
		PA: uint64(slot.PA + sidecarReqOffset), Len: 16, Flags: 0,
	}

	bufFlags := uint16(0)
	if requestType == VIRTIO_BLK_T_IN {
		bufFlags = virtio.VIRTQ_DESC_F_WRITE // Device writes for read operations
	}
	chain.Descs[1] = virtio.DescSpec{
		PA: uint64(dataPA), Len: dataLen, Flags: bufFlags,
	}
	chain.Descs[2] = virtio.DescSpec{
		PA: uint64(slot.PA + sidecarStatusOffset), Len: 1, Flags: virtio.VIRTQ_DESC_F_WRITE,
	}

	// Clear IOComplete BEFORE making request visible to avoid race:
	// under HVF the interrupt can fire before we reach the blocking call.
	atomic.StoreUint32(&d.Eng.IOComplete, 0)

	tag := d.Eng.Submit(&chain)
	if tag == virtio.InvalidIOTag {
		klog.Errf("[VirtIO Block] ERROR: Engine submit failed (no free descriptors)\n")
		d.Sidecars.Release(slot)
		return 0xFFFF, ErrNoDescriptors
	}

	return uint16(tag), nil
}

// DoBlockIOComplete pops the used ring, checks status, and copies read data.
// Must be called after IOComplete is set (by IRQ or polling).
//
// slotIdx identifies which in-flight slot to complete (matching the slotIdx
// passed to DoBlockIOSubmit). For reads, copies data from the DMA slot into buf.
//
// PCI mode uses Engine + SidecarPool. MMIO mode uses legacy Queue + DMA page.
//
//go:nosplit
func (d *VirtIOBlockDevice) DoBlockIOComplete(requestType uint32, reqDescIdx uint16, buf []byte, slotIdx uint8) error {
	if d.MMIOBase != 0 {
		return d.completeLegacy(requestType, reqDescIdx, buf)
	}

	// --- PCI Engine path ---

	// Pop from used ring
	info := d.Eng.PopUsed()
	if info.Tag == virtio.InvalidIOTag || uint16(info.Tag) != reqDescIdx {
		// Diagnostic: show queue state to help debug HVF memory ordering issues
		usedVA := virtio.PointerToUintptr(unsafe.Pointer(d.Eng.VQ.Used))
		rawUsedIdx := asm.MmioRead16(usedVA + 2)
		klog.Errf("[VirtIO Block] ERROR: Unexpected descriptor index (got %d, expected %d) Used.Idx=%d LastUsedIdx=%d avail=%d\n",
			uint16(info.Tag), reqDescIdx, rawUsedIdx, d.Eng.VQ.LastUsedIdx, d.Eng.VQ.Available.Idx)
		d.Sidecars.Release(d.inFlightSidecars[slotIdx])
		return ErrDeviceError
	}

	// Check status from sidecar slot
	slot := d.inFlightSidecars[slotIdx]
	statusPtr := (*VirtIOBlockStatus)(unsafe.Pointer(slot.VA + sidecarStatusOffset))
	status := *statusPtr
	if status != VIRTIO_BLK_S_OK {
		klog.Errf("[VirtIO Block] ERROR: Bad status=%d type=%d\n", status, requestType)
		d.Sidecars.Release(slot)
		if status == VIRTIO_BLK_S_IOERR {
			return ErrIOError
		} else if status == VIRTIO_BLK_S_UNSUPP {
			return ErrUnsupported
		}
		return ErrDeviceError
	}

	// For reads, copy data from DMA slot into caller's buffer
	if requestType == VIRTIO_BLK_T_IN && len(buf) > 0 {
		dataVA := d.DmaPageVA + uintptr(slotIdx)*uintptr(d.BlockSizeBytes)
		copyBytes(unsafe.Pointer(&buf[0]), unsafe.Pointer(dataVA), uintptr(d.BlockSizeBytes))
	}

	d.Sidecars.Release(slot)
	return nil
}

// CompleteReadSlot checks the sidecar status byte and releases the sidecar slot
// for the given in-flight slot index. Called from the batch path after the caller
// has already popped the completion from the used ring via Engine.PopUsed().
// Does NOT copy data — the caller copies directly from DataSlotVA to userspace.
//
//go:nosplit
func (d *VirtIOBlockDevice) CompleteReadSlot(slotIdx uint8) error {
	slot := d.inFlightSidecars[slotIdx]
	statusPtr := (*VirtIOBlockStatus)(unsafe.Pointer(slot.VA + sidecarStatusOffset))
	status := *statusPtr
	d.Sidecars.Release(slot)
	if status != VIRTIO_BLK_S_OK {
		if status == VIRTIO_BLK_S_IOERR {
			return ErrIOError
		}
		if status == VIRTIO_BLK_S_UNSUPP {
			return ErrUnsupported
		}
		return ErrDeviceError
	}
	return nil
}

// DataSlotVA returns the kernel VA of the data buffer for the given slot index.
// The data at this address is valid after the device completes the I/O request.
//
//go:nosplit
func (d *VirtIOBlockDevice) DataSlotVA(slotIdx uint8) uintptr {
	return d.DmaPageVA + uintptr(slotIdx)*uintptr(d.BlockSizeBytes)
}

// MaxBatchSize returns the maximum number of sectors that can be in-flight
// simultaneously, limited by the DMA data page size and block size.
//
//go:nosplit
func (d *VirtIOBlockDevice) MaxBatchSize() uint8 {
	if d.BlockSizeBytes == 0 {
		return 1
	}
	n := uint32(4096) / d.BlockSizeBytes
	if n > MaxInFlight {
		n = MaxInFlight
	}
	if n == 0 {
		n = 1
	}
	return uint8(n)
}

// doBlockIO performs a block I/O operation using polling (fallback path).
// Used by ReadBlock/WriteBlock which are called during early boot (before
// interrupt-driven I/O is available) and by non-syscall kernel code.
//
//go:nosplit
func (d *VirtIOBlockDevice) doBlockIO(requestType uint32, lba uint64, buf []byte) error {
	// Temporarily disable async mode so the IRQ top-half doesn't drain
	// the used ring before our polling loop sees the completion.
	// SetAsyncMode is nil during early boot (before async is configured).
	var prevAsync uint32
	if d.SetAsyncMode != nil {
		prevAsync = d.SetAsyncMode(0)
	}

	// Drain any stale used ring entries left by failed async I/O.
	// If SysBlockSubmit was called but completions weren't consumed
	// (e.g., platform doesn't support async delivery), the used ring
	// will have entries that don't belong to our request. Drain them
	// now so our polling loop only sees our own completion.
	if d.MMIOBase == 0 {
		for d.Eng.HasUsed() {
			d.Eng.PopUsed()
		}
	}

	reqDescIdx, err := d.DoBlockIOSubmit(requestType, lba, buf, 0, 0, 0)
	if err != nil {
		if d.SetAsyncMode != nil {
			d.SetAsyncMode(prevAsync)
		}
		return err
	}

	if d.MMIOBase != 0 {
		// MMIO polling (legacy path)
		vq := &d.Queue
		const maxRetries = 500000
		mmioBase := d.MMIOBase
		for i := 0; i < maxRetries; i++ {
			if mmioBase != 0 {
				isr := asm.MmioRead32(mmioBase + 0x60)
				if isr&1 != 0 {
					asm.MmioWrite32(mmioBase+0x64, isr)
					if virtio.VirtqueueHasUsed(vq) {
						break
					}
				}
			} else {
				if virtio.VirtqueueHasUsed(vq) {
					break
				}
				asm.Wfi()
			}
		}
		if !virtio.VirtqueueHasUsed(vq) {
			if d.SetAsyncMode != nil {
				d.SetAsyncMode(prevAsync)
			}
			klog.Errf("[VirtIO Block] ERROR: I/O timeout (avail=%d used=%d LBA=%d)\n",
				vq.Available.Idx, vq.Used.Idx, lba)
			return ErrTimeout
		}
	} else {
		// PCI polling (Engine-based). Notify device, then poll for completion.
		// bootYieldForIO reads the ISR register (forcing a VM exit on HVF/TCG)
		// or does WFI/StiHlt.
		asm.Dsb()
		d.Eng.Notify()

		const maxWaits = 1000000
		for i := 0; i < maxWaits; i++ {
			if d.Eng.HasUsed() {
				break
			}
			bootYieldForIO()
		}
		if !d.Eng.HasUsed() {
			if d.SetAsyncMode != nil {
				d.SetAsyncMode(prevAsync)
			}
			klog.Errf("[VirtIO Block] ERROR: I/O timeout (LBA=%d)\n", lba)
			return ErrTimeout
		}
	}

	result := d.DoBlockIOComplete(requestType, reqDescIdx, buf, 0)
	if d.SetAsyncMode != nil {
		d.SetAsyncMode(prevAsync)
	}
	return result
}

// ============================================================================
// MMIO Legacy I/O (RISC-V fallback — not Engine-managed)
// ============================================================================

// submitLegacy is the legacy MMIO submit path. Uses d.Queue and d.DmaPage
// with the combined header+status+data layout.
//
//go:nosplit
func (d *VirtIOBlockDevice) submitLegacy(requestType uint32, lba uint64, buf []byte) (uint16, error) {
	vq := &d.Queue
	dmaVA := d.DmaPageVA
	dmaPA := d.DmaPagePA

	// Write request header into DMA page
	reqPtr := (*VirtIOBlockReq)(unsafe.Pointer(dmaVA + dmaReqOffset))
	reqPtr.Type = requestType
	reqPtr.Sector = lba

	// Initialize status byte to 0xFF (sentinel)
	statusPtr := (*VirtIOBlockStatus)(unsafe.Pointer(dmaVA + dmaStatusOffset))
	*statusPtr = 0xFF

	// For writes, copy caller's data into DMA buffer before submitting
	if requestType == VIRTIO_BLK_T_OUT {
		dmaBuf := unsafe.Pointer(dmaVA + dmaBufOffset)
		copyBytes(dmaBuf, unsafe.Pointer(&buf[0]), uintptr(d.BlockSizeBytes))
	}

	// Physical addresses are trivially known from the DMA page base
	reqPhys := uint64(dmaPA + dmaReqOffset)
	statusPhys := uint64(dmaPA + dmaStatusOffset)
	bufPhys := uint64(dmaPA + dmaBufOffset)

	// Allocate three descriptors (unlinked)
	reqDescIdx := virtio.VirtqueueAddDesc(vq, reqPhys, 16, 0, 0xFFFF)
	if reqDescIdx == 0xFFFF {
		klog.Errf("[VirtIO Block] ERROR: Failed to allocate request descriptor\n")
		return 0xFFFF, ErrNoDescriptors
	}

	bufFlags := uint16(0)
	if requestType == VIRTIO_BLK_T_IN {
		bufFlags = virtio.VIRTQ_DESC_F_WRITE // Device writes for read operations
	}
	bufDescIdx := virtio.VirtqueueAddDesc(vq, bufPhys, d.BlockSizeBytes, bufFlags, 0xFFFF)
	if bufDescIdx == 0xFFFF {
		klog.Errf("[VirtIO Block] ERROR: Failed to allocate buffer descriptor\n")
		virtio.VirtqueueFreeDescChain(vq, reqDescIdx)
		return 0xFFFF, ErrNoDescriptors
	}

	statusDescIdx := virtio.VirtqueueAddDesc(vq, statusPhys, 1, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if statusDescIdx == 0xFFFF {
		klog.Errf("[VirtIO Block] ERROR: Failed to allocate status descriptor\n")
		virtio.VirtqueueFreeDescChain(vq, reqDescIdx)
		virtio.VirtqueueFreeDescChain(vq, bufDescIdx)
		return 0xFFFF, ErrNoDescriptors
	}

	// Link descriptors: req -> buf -> status
	reqDesc := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(reqDescIdx)*unsafe.Sizeof(virtio.VirtQDesc{})))
	reqDesc.Next = bufDescIdx
	reqDesc.Flags |= virtio.VIRTQ_DESC_F_NEXT

	bufDesc := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(bufDescIdx)*unsafe.Sizeof(virtio.VirtQDesc{})))
	bufDesc.Next = statusDescIdx
	bufDesc.Flags |= virtio.VIRTQ_DESC_F_NEXT

	// Memory barrier before notifying device
	asm.Dsb()

	// Clear IOComplete BEFORE notify to avoid race: under HVF the interrupt
	// can fire before we reach the blocking call.
	atomic.StoreUint32(&d.IOComplete, 0)

	// Submit to available ring and notify device
	virtio.VirtqueueAddToAvailable(vq, reqDescIdx)
	asm.Dsb()
	virtioBlockNotify(0)

	return reqDescIdx, nil
}

// completeLegacy is the legacy MMIO complete path. Uses d.Queue and d.DmaPage
// with the combined header+status+data layout.
//
//go:nosplit
func (d *VirtIOBlockDevice) completeLegacy(requestType uint32, reqDescIdx uint16, buf []byte) error {
	vq := &d.Queue

	// Pop from used ring
	usedDescIdx, _ := virtio.VirtqueueGetUsed(vq)
	if uint16(usedDescIdx) != reqDescIdx {
		// Diagnostic: show queue state to help debug HVF memory ordering issues
		usedVA := virtio.PointerToUintptr(unsafe.Pointer(vq.Used))
		rawUsedIdx := asm.MmioRead16(usedVA + 2)
		klog.Errf("[VirtIO Block] ERROR: Unexpected descriptor index (got %d, expected %d) Used.Idx=%d LastUsedIdx=%d avail=%d\n",
			usedDescIdx, reqDescIdx, rawUsedIdx, vq.LastUsedIdx, vq.Available.Idx)
		return ErrDeviceError
	}

	// Free the descriptor chain
	virtio.VirtqueueFreeDescChain(vq, reqDescIdx)

	// Check status
	statusPtr := (*VirtIOBlockStatus)(unsafe.Pointer(d.DmaPageVA + dmaStatusOffset))
	status := *statusPtr
	if status != VIRTIO_BLK_S_OK {
		klog.Errf("[VirtIO Block] ERROR: Bad status=%d type=%d\n", status, requestType)
		if status == VIRTIO_BLK_S_IOERR {
			return ErrIOError
		} else if status == VIRTIO_BLK_S_UNSUPP {
			return ErrUnsupported
		}
		return ErrDeviceError
	}

	// For reads, copy data from DMA buffer into caller's buffer
	if requestType == VIRTIO_BLK_T_IN {
		dmaBuf := unsafe.Pointer(d.DmaPageVA + dmaBufOffset)
		copyBytes(unsafe.Pointer(&buf[0]), dmaBuf, uintptr(d.BlockSizeBytes))
	}

	return nil
}

// ============================================================================
// Shared helpers
// ============================================================================

// copyBytes copies n bytes from src to dst.
//
//go:nosplit
func copyBytes(dst, src unsafe.Pointer, n uintptr) {
	// Copy 8 bytes at a time for the bulk
	d := uintptr(dst)
	s := uintptr(src)
	for n >= 8 {
		*(*uint64)(unsafe.Pointer(d)) = *(*uint64)(unsafe.Pointer(s))
		d += 8
		s += 8
		n -= 8
	}
	// Copy remaining bytes
	for n > 0 {
		*(*byte)(unsafe.Pointer(d)) = *(*byte)(unsafe.Pointer(s))
		d++
		s++
		n--
	}
}

// virtioBlockNotify notifies the device about new descriptors.
// Used only by the MMIO legacy path. PCI Engine path uses Engine.Notify().
// queueIndex: index of the queue to notify (typically 0)
//
//go:nosplit
func virtioBlockNotify(queueIndex uint16) {
	dev := &virtioBlockDevice
	if dev.MMIOBase != 0 {
		// MMIO transport: write queue index to QueueNotify register (offset 0x50)
		*(*uint32)(unsafe.Pointer(dev.MMIOBase + 0x50)) = uint32(queueIndex)
	} else {
		// PCI transport: calculate notify address
		// notify_addr = notify_base + (queue_notify_off * notify_off_multiplier)
		notifyAddr := dev.NotifyBase +
			uintptr(dev.QueueNotifyOff)*uintptr(dev.NotifyConfig.NotifyOffMultiplier)
		*(*uint16)(unsafe.Pointer(notifyAddr)) = queueIndex
	}

	// Memory barrier to ensure write completes
	asm.Dsb()
}

// Error types for block I/O
var (
	ErrBufferTooSmall = &blockError{"buffer too small"}
	ErrInvalidSize    = &blockError{"invalid buffer size"}
	ErrNoDescriptors  = &blockError{"no available descriptors"}
	ErrTimeout        = &blockError{"I/O timeout"}
	ErrIOError        = &blockError{"device I/O error"}
	ErrUnsupported    = &blockError{"unsupported operation"}
	ErrDeviceError    = &blockError{"device error"}
)

type blockError struct {
	msg string
}

func (e *blockError) Error() string {
	return e.msg
}
