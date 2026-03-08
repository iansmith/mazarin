package block

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio"
)

// ReadBlock reads a single block at the given LBA
// buf must be at least BlockSize() bytes
//
//go:nosplit
func (d *VirtIOBlockDevice) ReadBlock(lba uint64, buf []byte) error {
	if uint64(len(buf)) < d.BlockSize() {
		console.KPrintln("[VirtIO Block] ERROR: Buffer too small for read")
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
		console.KPrintln("[VirtIO Block] ERROR: Buffer size must equal block size for write")
		return ErrInvalidSize
	}

	return d.doBlockIO(VIRTIO_BLK_T_OUT, lba, buf)
}

// DMA page layout offsets (within a single 4KB page)
const (
	dmaReqOffset    = 0  // VirtIOBlockReq header: 16 bytes
	dmaStatusOffset = 16 // Status byte: 1 byte
	dmaBufOffset    = 32 // Data buffer: 512 bytes (aligned to 32)
)

// DoBlockIOSubmit sets up descriptors and submits a block I/O request.
// Returns the head descriptor index, or 0xFFFF on error.
// Clears IOComplete before notify so the IRQ handler can set it.
//
//go:nosplit
func (d *VirtIOBlockDevice) DoBlockIOSubmit(requestType uint32, lba uint64, buf []byte) (uint16, error) {
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
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate request descriptor")
		return 0xFFFF, ErrNoDescriptors
	}

	bufFlags := uint16(0)
	if requestType == VIRTIO_BLK_T_IN {
		bufFlags = virtio.VIRTQ_DESC_F_WRITE // Device writes for read operations
	}
	bufDescIdx := virtio.VirtqueueAddDesc(vq, bufPhys, d.BlockSizeBytes, bufFlags, 0xFFFF)
	if bufDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate buffer descriptor")
		virtio.VirtqueueFreeDescChain(vq, reqDescIdx)
		return 0xFFFF, ErrNoDescriptors
	}

	statusDescIdx := virtio.VirtqueueAddDesc(vq, statusPhys, 1, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if statusDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate status descriptor")
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

// DoBlockIOComplete pops the used ring, checks status, and copies read data.
// Must be called after IOComplete is set (by IRQ or polling).
//
//go:nosplit
func (d *VirtIOBlockDevice) DoBlockIOComplete(requestType uint32, reqDescIdx uint16, buf []byte) error {
	vq := &d.Queue

	// Pop from used ring
	usedDescIdx, _ := virtio.VirtqueueGetUsed(vq)
	if uint16(usedDescIdx) != reqDescIdx {
		console.KPrintf("[VirtIO Block] ERROR: Unexpected descriptor index (got %d, expected %d)\n",
			usedDescIdx, reqDescIdx)
		return ErrDeviceError
	}

	// Free the descriptor chain
	virtio.VirtqueueFreeDescChain(vq, reqDescIdx)

	// Check status
	statusPtr := (*VirtIOBlockStatus)(unsafe.Pointer(d.DmaPageVA + dmaStatusOffset))
	status := *statusPtr
	if status != VIRTIO_BLK_S_OK {
		console.KPrintf("[VirtIO Block] ERROR: Bad status=%d type=%d\n", status, requestType)
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

// doBlockIO performs a block I/O operation using polling (fallback path).
// Used by ReadBlock/WriteBlock which are called during early boot (before
// interrupt-driven I/O is available) and by non-syscall kernel code.
//
//go:nosplit
func (d *VirtIOBlockDevice) doBlockIO(requestType uint32, lba uint64, buf []byte) error {
	reqDescIdx, err := d.DoBlockIOSubmit(requestType, lba, buf)
	if err != nil {
		return err
	}

	vq := &d.Queue

	// Wait for I/O completion via polling.
	if d.IRQNum != 0 {
		const maxWaits = 1000000
		for i := 0; i < maxWaits; i++ {
			if atomic.LoadUint32(&d.IOComplete) != 0 {
				break
			}
			if virtio.VirtqueueHasUsed(vq) {
				break
			}
			asm.Wfe()
		}
		if atomic.LoadUint32(&d.IOComplete) == 0 && !virtio.VirtqueueHasUsed(vq) {
			console.KPrintf("[VirtIO Block] ERROR: I/O timeout (avail=%d used=%d LBA=%d)\n",
				vq.Available.Idx, vq.Used.Idx, lba)
			return ErrTimeout
		}
	} else if d.ISRBase != 0 {
		const maxWaits = 100000
		for i := 0; i < maxWaits; i++ {
			isr := asm.MmioRead8(d.ISRBase)
			if isr&1 != 0 {
				break
			}
		}
		if !virtio.VirtqueueHasUsed(vq) {
			console.KPrintf("[VirtIO Block] ERROR: I/O timeout (avail=%d used=%d LBA=%d)\n",
				vq.Available.Idx, vq.Used.Idx, lba)
			return ErrTimeout
		}
	} else {
		// MMIO transport: poll InterruptStatus + check used ring.
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
			console.KPrintf("[VirtIO Block] ERROR: I/O timeout (avail=%d used=%d LBA=%d)\n",
				vq.Available.Idx, vq.Used.Idx, lba)
			return ErrTimeout
		}
	}

	return d.DoBlockIOComplete(requestType, reqDescIdx, buf)
}

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

// virtioBlockNotify notifies the device about new descriptors
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
