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

// doBlockIO performs a block I/O operation (read or write) using the
// pre-allocated DMA page. All descriptor physical addresses are known
// at init time, completely avoiding demand-paging page table walks.
//
// requestType: VIRTIO_BLK_T_IN (read) or VIRTIO_BLK_T_OUT (write)
// lba: logical block address (sector number)
// buf: caller's data buffer (must be at least BlockSize bytes)
//
//go:nosplit
func (d *VirtIOBlockDevice) doBlockIO(requestType uint32, lba uint64, buf []byte) error {
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
		return ErrNoDescriptors
	}

	bufFlags := uint16(0)
	if requestType == VIRTIO_BLK_T_IN {
		bufFlags = virtio.VIRTQ_DESC_F_WRITE // Device writes for read operations
	}
	bufDescIdx := virtio.VirtqueueAddDesc(vq, bufPhys, d.BlockSizeBytes, bufFlags, 0xFFFF)
	if bufDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate buffer descriptor")
		virtio.VirtqueueFreeDescChain(vq, reqDescIdx)
		return ErrNoDescriptors
	}

	statusDescIdx := virtio.VirtqueueAddDesc(vq, statusPhys, 1, virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if statusDescIdx == 0xFFFF {
		console.KPrintln("[VirtIO Block] ERROR: Failed to allocate status descriptor")
		virtio.VirtqueueFreeDescChain(vq, reqDescIdx)
		virtio.VirtqueueFreeDescChain(vq, bufDescIdx)
		return ErrNoDescriptors
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

	// Submit to available ring and notify device.
	// Clear IOComplete BEFORE notify to avoid race: under HVF the interrupt
	// can fire before we reach the WFI loop.
	if d.IRQNum != 0 {
		atomic.StoreUint32(&d.IOComplete, 0)
	}
	virtio.VirtqueueAddToAvailable(vq, reqDescIdx)
	asm.Dsb()

	virtioBlockNotify(0)

	// Wait for I/O completion. Three paths:
	//
	// 1. Interrupt-driven (IRQNum != 0): WFE yields to the hypervisor,
	//    giving QEMU's event loop time to process the I/O. The INTx
	//    interrupt fires when the device completes, and the top-half
	//    handler sets IOComplete. Under HVF, WFE causes a VM exit
	//    (trapped via HCR_EL2.TWE) and returns immediately — each
	//    iteration is a cooperative yield to the hypervisor.
	//
	// 2. ISR polling (ISRBase != 0, IRQNum == 0): Read the VirtIO ISR
	//    register. Each MMIO read causes a VM exit, and ISR bit 0
	//    indicates used buffer notification.
	//
	// 3. MMIO transport (ISRBase == 0, RISC-V): WFI + check used ring.
	if d.IRQNum != 0 {
		const maxWaits = 1000000
		for i := 0; i < maxWaits; i++ {
			if atomic.LoadUint32(&d.IOComplete) != 0 {
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
		// MMIO transport: WFI + check used ring directly
		const maxRetries = 5000
		for i := 0; i < maxRetries; i++ {
			if virtio.VirtqueueHasUsed(vq) {
				break
			}
			yieldForIO()
		}
		if !virtio.VirtqueueHasUsed(vq) {
			console.KPrintf("[VirtIO Block] ERROR: I/O timeout (avail=%d used=%d LBA=%d)\n",
				vq.Available.Idx, vq.Used.Idx, lba)
			return ErrTimeout
		}
	}

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
	status := *statusPtr
	if status != VIRTIO_BLK_S_OK {
		console.KPrintf("[VirtIO Block] ERROR: Bad status=%d LBA=%d type=%d\n", status, lba, requestType)
		if status == VIRTIO_BLK_S_IOERR {
			return ErrIOError
		} else if status == VIRTIO_BLK_S_UNSUPP {
			return ErrUnsupported
		}
		return ErrDeviceError
	}

	// For reads, copy data from DMA buffer into caller's buffer
	if requestType == VIRTIO_BLK_T_IN {
		dmaBuf := unsafe.Pointer(dmaVA + dmaBufOffset)
		copyBytes(unsafe.Pointer(&buf[0]), dmaBuf, uintptr(d.BlockSizeBytes))
	}

	return nil
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
