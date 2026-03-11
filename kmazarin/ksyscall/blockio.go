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
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
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
			serial.RawUARTPuts("[BlockRead] EFAULT at LBA ")
			serial.RawUARTDecimal(lba)
			serial.RawUARTPuts(" dstVA=0x")
			serial.RawUARTHex64(uint64(dstVA))
			serial.RawUARTPuts(" l0PA=0x")
			serial.RawUARTHex64(uint64(l0PA))
			serial.RawUARTPuts("\r\n")
			return -14 // EFAULT
		}
	}

	return 0
}

// blockReadInterrupt performs a single block read using interrupt-driven I/O.
// Submits the request, waits for the MSI-X/INTx interrupt to signal completion
// via IOComplete, then reads the result from the used ring.
//
// Runs synchronously within the SVC handler using EnableIRQsAndWait
// (DAIFClr + WFI + DAIFSet) to halt the CPU until the block device interrupt
// fires. svcDepth=1 prevents timer preemption during the SVC handler, so
// only device interrupts are processed. Interrupt-driven, not a busy-loop;
// completes in microseconds.
//
//go:noinline
func blockReadInterrupt(dev *block.VirtIOBlockDevice, lba uint64, buf []byte) error {
	if uint64(len(buf)) < dev.BlockSize() {
		return block.ErrBufferTooSmall
	}

	reqDescIdx, err := dev.DoBlockIOSubmit(block.VIRTIO_BLK_T_IN, lba, buf)
	if err != nil {
		return err
	}

	// Wait for I/O completion via interrupt. EnableIRQsAndWait enables IRQs,
	// executes WFI (CPU halts until interrupt), then re-disables IRQs.
	//
	// We check VirtqueueHasUsed (the actual used ring) rather than IOComplete
	// because under HVF, stale MSI-X interrupts from a previous polling-path
	// I/O can set IOComplete prematurely before the current request completes.
	// The polling path (doBlockIO, used during early boot) leaves interrupts
	// pending in the GIC; when we unmask IRQs here, the stale interrupt fires
	// and sets IOComplete even though Used.Idx hasn't been updated for our
	// request. VirtqueueHasUsed checks the used ring directly, so stale
	// interrupts just cause a harmless extra WFI iteration.
	//
	// Still interrupt-driven: WFI halts until an interrupt wakes us, we just
	// verify the used ring rather than trusting a flag.
	vq := &dev.Queue
	for !virtio.VirtqueueHasUsed(vq) {
		enableIRQsAndWait()
	}

	// DMA read barrier: ensure the device's used ring DMA writes are visible
	// before we read them in DoBlockIOComplete. Under HVF, the VirtIO backend
	// runs on a separate host thread; this barrier orders their DMA writes
	// before our reads. (EnableIRQsAndWait also has a DMB, but belt-and-suspenders.)
	asm.DmaRmb()

	return dev.DoBlockIOComplete(block.VIRTIO_BLK_T_IN, reqDescIdx, buf)
}

// copyToUser copies data from a kernel buffer to a userspace VA.
// Works page-by-page: for each destination page, ensures it is mapped
// (demand-faulting if needed), then copies through the kernel scratch mapping.
//
//go:noinline
func copyToUser(userVA uintptr, data []byte, l0PA uintptr) bool {
	remaining := len(data)
	offset := 0

	for remaining > 0 {
		// Resolve the physical page for this VA, demand-faulting if unmapped
		va := userVA + uintptr(offset)
		pa := kmem.DemandMapUserPage(va, l0PA)
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
