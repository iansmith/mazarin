package ksyscall

// blockio.go — SysBlockRead kernel syscall implementation.
//
// Provides kernel-mediated block device I/O for the disk priest.
// The disk priest cannot drive VirtIO hardware directly (DMA requires
// physical addresses and device register access), so it uses this syscall
// to read sectors through the kernel's block device driver.
//
// When the block device has an IRQ configured, uses interrupt-driven I/O:
// submit request, block the calling thread, IRQ wakes it on completion.
// Falls back to polling (ReadBlock) when no IRQ is available.

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// SyscallBlockRead reads disk sectors into the caller's buffer.
// Restricted to the priest that registered for BlockVirtualIRQ.
//
// arg0 = startLBA    (first sector to read)
// arg1 = numSectors  (number of 512-byte sectors to read)
// arg2 = bufVA       (destination buffer in caller's address space)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallBlockRead(arg0, arg1, arg2, _, _, _ uint64) int64 {
	startLBA := arg0
	numSectors := arg1
	bufVA := uintptr(arg2)

	// Validate caller is the block device owner
	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}
	ownerPID := getBlockDeviceOwnerPID()
	if ownerPID < 0 || int16(callerPriest.PID) != ownerPID {
		serial.RawUARTPuts("[BlockRead] EPERM: not block device owner\r\n")
		return -1 // EPERM
	}

	if numSectors == 0 || numSectors > 256 {
		return -22 // EINVAL
	}

	// Get the kernel block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KPrintln("[BlockRead] ERROR: no block device")
		return -19 // ENODEV
	}

	blockSize := blk.BlockSize()
	if blockSize == 0 {
		return -19 // ENODEV
	}

	l0PA := callerPriest.PageTableL0PA

	// Check if interrupt-driven I/O is available
	dev := block.GetDevice()
	useInterrupt := dev != nil && dev.IRQNum != 0

	// Read each sector and copy to userspace
	var sectorBuf [512]byte
	for i := uint64(0); i < numSectors; i++ {
		lba := startLBA + i
		buf := sectorBuf[:blockSize]

		var err error
		if useInterrupt {
			err = blockReadInterrupt(dev, lba, buf)
		} else {
			err = blk.ReadBlock(lba, buf)
		}
		if err != nil {
			serial.RawUARTPuts("[BlockRead] I/O error at LBA ")
			serial.RawUARTDecimal(lba)
			serial.RawUARTPuts("\r\n")
			return -5 // EIO
		}

		// Copy sector data to userspace via scratch mapping
		dstVA := bufVA + uintptr(i*blockSize)
		if !copyToUser(dstVA, buf, l0PA) {
			return -14 // EFAULT
		}
	}

	return 0
}

// blockReadInterrupt performs a single block read using interrupt-driven I/O.
// Submits the request, blocks the thread until the IRQ fires, then completes.
// BlockForBlockIO switches to thread 0 (KernelIdleLoop), which WFIs until the
// block IRQ fires and wakes us back. Returns 0 if I/O completed before blocking.
//
//go:noinline
func blockReadInterrupt(dev *block.VirtIOBlockDevice, lba uint64, buf []byte) error {
	if uint64(len(buf)) < dev.BlockSize() {
		return block.ErrBufferTooSmall
	}

	// Store current TID so the IRQ top-half can wake us
	_, tid := getCurrentThreadPIDAndTID()
	atomic.StoreInt32(&dev.BlockedTID, int32(tid))

	reqDescIdx, err := dev.DoBlockIOSubmit(block.VIRTIO_BLK_T_IN, lba, buf)
	if err != nil {
		atomic.StoreInt32(&dev.BlockedTID, -1)
		return err
	}

	// Block until I/O completes. BlockForBlockIO re-checks IOComplete under
	// the scheduler lock to prevent the missed-wakeup race. Normally switches
	// to thread 0 (KernelIdleLoop) which WFIs until the block IRQ fires and
	// WakeBlockIOThread marks us ready. Returns 0 if I/O already completed.
	nextCtx := BlockForBlockIO(&dev.IOComplete)
	if nextCtx != 0 {
		// Context switch happens in the SVC return path.
		// When we resume, I/O is complete.
		SetSyscallSwitchTarget(nextCtx)
	}

	// Clear our TID from the device
	atomic.StoreInt32(&dev.BlockedTID, -1)

	return dev.DoBlockIOComplete(block.VIRTIO_BLK_T_IN, reqDescIdx, buf)
}

// copyToUser copies data from a kernel buffer to a userspace VA
// using the kernel scratch mapping.
//
//go:noinline
func copyToUser(userVA uintptr, data []byte, l0PA uintptr) bool {
	remaining := len(data)
	offset := 0

	for remaining > 0 {
		// Find the physical page for this VA
		va := userVA + uintptr(offset)
		pa := kmem.WalkUserPageTableWithL0(va, l0PA)
		if pa == 0 {
			return false
		}

		pagePA := pa &^ (kmem.PageSize - 1)
		pageOffset := va & (kmem.PageSize - 1)
		kernelVA := kmem.MapPAToKernelScratch(pagePA)
		if kernelVA == 0 {
			return false
		}

		// Copy up to the end of this page
		canCopy := int(kmem.PageSize - pageOffset)
		if canCopy > remaining {
			canCopy = remaining
		}

		dst := unsafe.Slice((*byte)(unsafe.Pointer(kernelVA+pageOffset)), canCopy)
		copy(dst, data[offset:offset+canCopy])

		offset += canCopy
		remaining -= canCopy
	}

	return true
}
