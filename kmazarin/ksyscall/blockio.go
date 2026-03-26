package ksyscall

// blockio.go — SysBlockRead kernel syscall implementation.
//
// Provides kernel-mediated block device I/O for the disk shepherd.
// The disk shepherd cannot drive VirtIO hardware directly (DMA requires
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
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"unsafe"
)

// SyscallBlockRead reads disk sectors into the caller's buffer.
// Restricted to the shepherd that registered for BlockVirtualIRQ.
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
	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	ownerSID := getBlockDeviceOwnerPID()
	if ownerSID < 0 || int16(callerShepherd.PID) != ownerSID {
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

	l0PA := callerShepherd.PageTableL0PA

	// Check if interrupt-driven I/O is available
	dev := block.GetDevice()
	useInterrupt := dev != nil && dev.IRQNum != 0

	if useInterrupt {
		return blockReadBatch(dev, startLBA, numSectors, bufVA, blockSize, l0PA)
	}

	// Fallback: sector-by-sector polling (boot path, no interrupts)
	var sectorBuf [512]byte
	for i := uint64(0); i < numSectors; i++ {
		lba := startLBA + i
		buf := sectorBuf[:blockSize]

		if err := blk.ReadBlock(lba, buf); err != nil {
			serial.RawUARTPuts("[BlockRead] I/O error at LBA ")
			serial.RawUARTDecimal(lba)
			serial.RawUARTPuts("\r\n")
			return -5 // EIO
		}

		dstVA := bufVA + uintptr(i*blockSize)
		if !copyToUser(dstVA, buf, l0PA) {
			serial.RawUARTPuts("[BlockRead] EFAULT at LBA ")
			serial.RawUARTDecimal(lba)
			serial.RawUARTPuts("\r\n")
			return -14 // EFAULT
		}
	}

	return 0
}

// blockReadBatch reads sectors using batched interrupt-driven I/O.
// Submits up to MaxBatchSize sectors at once, single notify, then waits
// for all completions via WFI loop.
//
// Two data paths:
//   - Zero-copy (DMA pool): if the caller's shepherd has a registered DMA pool
//     and bufVA falls within it, I/O goes directly to the userspace page.
//     After completion, InvalidateDCacheRange ensures the CPU sees device data.
//   - Copy path (default): I/O goes to a kernel DMA page slot, then copyToUser
//     transfers data to the caller's address space.
//
// Uses EnableIRQsAndWait (DAIFClr + WFI + DAIFSet) to halt the CPU until
// the block device interrupt fires. svcDepth=1 prevents timer preemption
// during the SVC handler, so only device interrupts are processed.
//
//go:noinline
func blockReadBatch(dev *block.VirtIOBlockDevice, startLBA, numSectors uint64,
	bufVA uintptr, blockSize uint64, l0PA uintptr) int64 {

	maxBatch := uint64(dev.MaxBatchSize())
	freq := uint64(kirq.GetTimerFrequency())

	// Check for DMA pool zero-copy path.
	// If the caller has a registered pool and the buffer falls within it,
	// we submit I/O directly to the userspace pages (no kernel copy).
	shepherd := proc.CurrentShepherd()
	var pool *proc.DMAPool
	zeroCopy := false
	if shepherd != nil {
		pool = shepherd.DMAPool
		if pool != nil {
			// Verify the entire buffer range falls within the pool
			endVA := bufVA + uintptr(numSectors*blockSize) - 1
			if pool.LookupPA(bufVA) != 0 && pool.LookupPA(endVA) != 0 {
				zeroCopy = true
			}
		}
	}

	for sectorsRead := uint64(0); sectorsRead < numSectors; {
		// Determine batch size for this iteration
		batch := numSectors - sectorsRead
		if batch > maxBatch {
			batch = maxBatch
		}

		// Submit batch — one request per slot, single notify at end
		var tags [block.MaxInFlight]uint16
		for i := uint64(0); i < batch; i++ {
			lba := startLBA + sectorsRead + i

			var extDataPA uintptr
			if zeroCopy {
				// Look up the cached PA for this userspace page
				va := bufVA + uintptr((sectorsRead+uint64(i))*blockSize)
				extDataPA = pool.LookupPA(va)
				if extDataPA == 0 {
					serial.RawUARTPuts("[BlockRead] DMA pool lookup failed at VA ")
					serial.RawUARTHex64(uint64(va))
					serial.RawUARTPuts("\r\n")
					return -14 // EFAULT
				}
				// Clean cache before device write (device will overwrite this region)
				kernelVA := extDataPA + constants.KernelMMIOOffset
				asm.CleanDCacheRange(kernelVA, uintptr(blockSize))
			}

			tag, err := dev.DoBlockIOSubmit(block.VIRTIO_BLK_T_IN, lba, nil, uint8(i), extDataPA)
			if err != nil {
				serial.RawUARTPuts("[BlockRead] submit error at LBA ")
				serial.RawUARTDecimal(lba)
				serial.RawUARTPuts("\r\n")
				return -5 // EIO
			}
			tags[i] = tag
		}

		asm.Dsb()
		dev.Eng.Notify()

		// Wait for all completions. Uses WFI (interrupt-driven, not polling).
		// Timeout: 5 seconds per batch.
		deadline := kirq.ReadCounterValue() + freq*5
		completed := uint64(0)

		for completed < batch {
			// Wait for at least one used ring entry
			for !dev.Eng.HasUsed() {
				if kirq.ReadCounterValue() > deadline {
					serial.RawUARTPuts("[BlockRead] TIMEOUT in batch\r\n")
					return -5 // EIO
				}
				enableIRQsAndWait()
			}

			asm.DmaRmb()

			// Drain all available completions
			for dev.Eng.HasUsed() && completed < batch {
				info := dev.Eng.PopUsed()
				if info.Tag == virtio.InvalidIOTag {
					break
				}

				// Find which batch slot this completion belongs to
				slotIdx := int(-1)
				for j := uint64(0); j < batch; j++ {
					if tags[j] == uint16(info.Tag) {
						slotIdx = int(j)
						break
					}
				}
				if slotIdx < 0 {
					serial.RawUARTPuts("[BlockRead] unexpected tag in batch\r\n")
					return -5 // EIO
				}

				// Check sidecar status and release sidecar slot
				if err := dev.CompleteReadSlot(uint8(slotIdx)); err != nil {
					serial.RawUARTPuts("[BlockRead] I/O error in batch at LBA ")
					serial.RawUARTDecimal(startLBA + sectorsRead + uint64(slotIdx))
					serial.RawUARTPuts("\r\n")
					return -5 // EIO
				}

				if zeroCopy {
					// Invalidate cache so CPU sees device-written data
					va := bufVA + uintptr((sectorsRead+uint64(slotIdx))*blockSize)
					pa := pool.LookupPA(va)
					kernelVA := pa + constants.KernelMMIOOffset
					asm.InvalidateDCacheRange(kernelVA, uintptr(blockSize))
					asm.DmaRmb()
				} else {
					// Copy data from DMA slot directly to userspace
					dataVA := dev.DataSlotVA(uint8(slotIdx))
					srcBuf := unsafe.Slice((*byte)(unsafe.Pointer(dataVA)), int(blockSize))
					dstVA := bufVA + uintptr((sectorsRead+uint64(slotIdx))*blockSize)
					if !copyToUser(dstVA, srcBuf, l0PA) {
						serial.RawUARTPuts("[BlockRead] EFAULT in batch at LBA ")
						serial.RawUARTDecimal(startLBA + sectorsRead + uint64(slotIdx))
						serial.RawUARTPuts("\r\n")
						return -14 // EFAULT
					}
				}

				// Mark tag as completed to avoid double-match
				tags[slotIdx] = 0xFFFF
				completed++
			}
		}

		sectorsRead += batch
	}

	return 0
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
