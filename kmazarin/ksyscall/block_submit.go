package ksyscall

// block_submit.go — SysBlockSubmit async block I/O syscall (Phase 4).
//
// Submits a single block I/O request and returns immediately with an IOTag.
// The request completes asynchronously — the IRQ top-half drains the engine
// used ring, pushes completion events to the block softIRQ ring, and wakes
// any thread blocked on WaitSoftIRQ. Userspace receives completions as
// HIDEvents with Type=IOTag, Code=status, Value=usedLen.
//
// Requires a registered DMA pool (Phase 3) — I/O goes directly to userspace
// pages (zero-copy).

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

// blockAsyncEnabled tracks whether async mode has been activated.
// Set to 1 on first SysBlockSubmit call.
var blockAsyncEnabled uint32

// SyscallBlockSubmit submits an async block I/O request.
//
// arg0 = requestType  (0 = read, 1 = write)
// arg1 = startLBA     (first sector)
// arg2 = numSectors   (number of sectors, must fit in one DMA pool page)
// arg3 = bufVA        (destination/source buffer — must be in a DMA clump)
// arg4 = targetSID    (shepherd whose DMA clump contains bufVA;
//
//	0 = caller's own clumps)
//
// Returns: IOTag (>= 0) on success, negative errno on error.
// Does NOT block — returns immediately after submitting to device.
//
//go:noinline
func SyscallBlockSubmit(arg0, arg1, arg2, arg3, arg4, _ uint64) int64 {
	requestType := uint32(arg0)
	startLBA := arg1
	numSectors := arg2
	bufVA := uintptr(arg3)
	targetSID := uint16(arg4)

	// Validate request type
	if requestType != block.VIRTIO_BLK_T_IN && requestType != block.VIRTIO_BLK_T_OUT {
		return -22 // EINVAL
	}

	// Validate caller is the block device owner
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}
	ownerSID := getBlockDeviceOwnerPID()
	if ownerSID < 0 || int16(shepherd.PID) != ownerSID {
		return -1 // EPERM
	}

	// Get block device
	_, ok := device.GetBlockDevice()
	if !ok {
		return -19 // ENODEV
	}
	dev := block.GetDevice()
	if dev == nil || dev.IRQNum == 0 {
		return -19 // ENODEV
	}

	blockSize := uint64(dev.BlockSizeBytes)
	if blockSize == 0 {
		return -19 // ENODEV
	}

	// Validate numSectors fits in the buffer
	totalBytes := numSectors * blockSize
	if numSectors == 0 || totalBytes > 4096 {
		serial.RawUARTPuts("[BlockSubmit] EINVAL: numSectors out of range\r\n")
		return -22 // EINVAL
	}

	// Resolve bufVA → PA via DMA clump. When targetSID != 0, look up
	// the clump in the target shepherd's DMA clump list (cross-shepherd DMA).
	var clumpOwner *proc.Shepherd
	if targetSID != 0 {
		clumpOwner = proc.FindShepherdBySID(proc.ShepherdId(targetSID))
		if clumpOwner == nil {
			serial.RawUARTPuts("[BlockSubmit] ESRCH: target shepherd not found\r\n")
			return -3 // ESRCH
		}
	} else {
		clumpOwner = shepherd
	}
	clump := clumpOwner.FindClumpByVA(bufVA)
	if clump == nil {
		serial.RawUARTPuts("[BlockSubmit] EFAULT: bufVA not in any DMA clump\r\n")
		return -14 // EFAULT
	}
	pa := clump.LookupPA(bufVA)
	endPA := clump.LookupPA(bufVA + uintptr(totalBytes) - 1)
	if pa == 0 || endPA == 0 {
		serial.RawUARTPuts("[BlockSubmit] EFAULT: buffer extends beyond clump\r\n")
		return -14 // EFAULT
	}
	atomic.AddInt32(&clump.InFlight, 1)

	// Enable async mode on first call
	if atomic.LoadUint32(&blockAsyncEnabled) == 0 {
		enableBlockAsyncMode()
		atomic.StoreUint32(&blockAsyncEnabled, 1)
	}

	// For writes, clean cache so device sees userspace data
	kernelVA := pa + constants.KernelMMIOOffset
	if requestType == block.VIRTIO_BLK_T_OUT {
		asm.CleanDCacheRange(kernelVA, uintptr(totalBytes))
		asm.DmaWmb()
	}

	// Submit via engine — use slotIdx=0 (async: one request at a time per submit call)
	// extDataPA = page-aligned PA from DMA pool
	tag, err := dev.DoBlockIOSubmit(requestType, startLBA, nil, 0, pa, uint32(totalBytes))
	if err != nil {
		serial.RawUARTPuts("[BlockSubmit] submit error\r\n")
		return -5 // EIO
	}

	// Store per-tag metadata for the top-half completion handler.
	// sidecarStatusVA: VA of the status byte in the sidecar slot.
	sidecarSlot := dev.GetInFlightSidecar(0)
	dataKernelVA := pa + constants.KernelMMIOOffset
	if requestType == block.VIRTIO_BLK_T_OUT {
		dataKernelVA = 0 // No cache invalidate needed for writes
	}
	setBlockAsyncSlot(tag, sidecarSlot.VA+16, sidecarSlot.Index, dataKernelVA, uint32(totalBytes), uintptr(unsafe.Pointer(clump)))

	// Notify device
	asm.Dsb()
	dev.Eng.Notify()

	return int64(tag)
}
