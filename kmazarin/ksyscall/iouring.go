package ksyscall

// iouring.go — SysIOUringSetup and SysIOUringEnter syscall handlers.
//
// Implements Linux-style io_uring for async block I/O. A single shared-memory
// page holds both submission (SQ) and completion (CQ) rings. IOUringEnter
// consumes SQEs, submits them to VirtIO, and blocks until minComplete CQEs
// are available. The IRQ top-half writes CQEs and wakes the blocked thread
// directly — no mailbox, no channel, no P-release.
//
// Uses RawSyscall from userspace so the Go P is held throughout. A 10ms
// safety timeout prevents hung hardware from freezing the shepherd forever.
//
// Ring state and blocking functions live in kmazarin/kmazarin/iouring.go
// (main package) so the IRQ top-half and scheduler have direct Thread access.
// This file contains the SVC dispatch handlers that bridge to that state.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/block"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"mazzy/shared/iouring"
	"mazzy/shared/mazzy"
	"sync/atomic"
	"unsafe"
)

// SyscallIOUringSetup creates an io_uring instance.
// arg0 = capacity hint (ignored, always 32 SQ / 64 CQ)
// arg1 = ringPageVA (userspace VA of a pre-allocated shared page)
// arg2 = flags: low byte = device type (0=block, 1=input)
// Returns: ringID (0-3) on success, negative errno on error.
//
//go:noinline
func SyscallIOUringSetup(arg0, arg1, arg2, _, _, _ uint64) int64 {
	ringPageVA := uintptr(arg1)
	deviceType := uint8(arg2 & 0xFF)

	if deviceType > 1 {
		return -22 // EINVAL — unknown device type
	}
	if ringPageVA == 0 || ringPageVA&0xFFF != 0 {
		return -22 // EINVAL — must be page-aligned
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	// Block device rings: register caller as block device owner.
	// Input device rings: no ownership check needed.
	if deviceType == 0 {
		ownerSID := getBlockDeviceOwnerPID()
		if ownerSID >= 0 && int16(shepherd.PID) != ownerSID {
			return -1 // EPERM — someone else owns the block device
		}
		setBlockDeviceOwnerPID(int16(shepherd.PID))
	}

	// Compute timeout ticks once.
	initIOUringTimeout()

	// Find a free slot.
	slotIdx := -1
	for i := 0; i < maxIORings(); i++ {
		slot := getIOUringSlot(i)
		if slot == nil || ioUringSlotKVA(i) == 0 {
			slotIdx = i
			break
		}
	}
	if slotIdx < 0 {
		return -16 // EBUSY — all slots in use
	}

	// Walk user page table to get PA.
	pa := kmem.WalkUserPageTable(ringPageVA)
	if pa == 0 {
		if !kmem.HandleUserPageFault(ringPageVA, 0) {
			return -14 // EFAULT
		}
		pa = kmem.WalkUserPageTable(ringPageVA)
		if pa == 0 {
			return -14 // EFAULT
		}
	}

	// Pin the page.
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil {
		return -14 // EFAULT
	}
	desc.Flags |= kmem.PD_PINNED
	desc.RefCount++

	// Map to kernel VA.
	kva := kmem.MapPAToKernelScratch(pa)
	if kva == 0 {
		desc.Flags &^= kmem.PD_PINNED
		desc.RefCount--
		return -14 // EFAULT
	}

	// Initialize the ring page.
	ring := (*iouring.IORing)(unsafe.Pointer(kva))
	ring.SQHead = 0
	ring.SQTail = 0
	ring.SQRingMask = iouring.SQMask
	ring.SQCount = iouring.SQCapacity
	ring.CQHead = 0
	ring.CQTail = 0
	ring.CQRingMask = iouring.CQMask
	ring.CQCount = iouring.CQCapacity
	ring.CQFlags = 0

	// Store kernel state.
	setupIOUringSlot(slotIdx, kva, pa, int16(shepherd.PID), deviceType)

	// Enable async block mode for block device rings only.
	if deviceType == 0 {
		enableBlockAsyncMode()
	}

	serial.RawUARTPuts("[IOUring] setup ring=")
	serial.RawUARTDecimal(uint64(slotIdx))
	serial.RawUARTPuts(" KVA=0x")
	serial.RawUARTHexCompact(uint64(kva))
	serial.RawUARTPuts(" SID=")
	serial.RawUARTDecimal(uint64(shepherd.PID))
	serial.RawUARTPuts("\r\n")

	return int64(slotIdx)
}

// SyscallIOUringEnter submits SQEs and waits for completions.
// arg0 = ringID
// arg1 = toSubmit (number of SQEs to consume)
// arg2 = minComplete (minimum CQEs before returning; 0 = don't wait)
// arg3 = flags (reserved)
// Returns: number of CQEs available, or negative errno.
//
//go:noinline
func SyscallIOUringEnter(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	ringID := int(arg0)
	toSubmit := uint32(arg1)
	minComplete := uint32(arg2)

	if ringID < 0 || ringID >= maxIORings() {
		return -22 // EINVAL
	}
	kva := ioUringSlotKVA(ringID)
	if kva == 0 {
		return -9 // EBADF — ring not set up
	}
	ownerSID := ioUringSlotOwnerSID(ringID)

	// Validate caller owns this ring.
	shepherd := proc.CurrentShepherd()
	if shepherd == nil || int16(shepherd.PID) != ownerSID {
		return -1 // EPERM
	}

	ring := (*iouring.IORing)(unsafe.Pointer(kva))

	// Phase A: Submit SQEs.
	submitted := uint32(0)
	sqHead := uint32(0) // hoisted for Phase B diagnostic
	if toSubmit > 0 {
		sqHead = atomic.LoadUint32(&ring.SQHead)
		sqTail := atomic.LoadUint32(&ring.SQTail)
		available := sqTail - sqHead
		if toSubmit > available {
			toSubmit = available
		}

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

		for i := uint32(0); i < toSubmit; i++ {
			idx := (sqHead + i) & iouring.SQMask
			sqe := &ring.SQEntries[idx]

			if sqe.Opcode == iouring.IOUringOpNop {
				continue
			}
			if sqe.Opcode != iouring.IOUringOpRead && sqe.Opcode != iouring.IOUringOpWrite {
				return -22 // EINVAL — unknown opcode
			}

			// Translate VA → PA via DMA clump (same logic as SyscallBlockSubmit).
			bufVA := uintptr(sqe.Addr)
			clump := shepherd.FindClumpByVA(bufVA)
			if clump == nil {
				serial.RawUARTPuts("[IOUring] EFAULT: VA not in clump\r\n")
				return -14 // EFAULT
			}
			totalBytes := uint64(sqe.Len) * blockSize
			pa := clump.LookupPA(bufVA)
			endPA := clump.LookupPA(bufVA + uintptr(totalBytes) - 1)
			if pa == 0 || endPA == 0 {
				return -14 // EFAULT
			}
			atomic.AddInt32(&clump.InFlight, 1)

			// Cache management: writes need clean (push dirty lines to RAM),
			// reads need pre-invalidate (discard dirty lines so DMA'd data
			// isn't overwritten by stale cache writeback).
			kernelVA := pa + constants.KernelMMIOOffset
			requestType := block.VIRTIO_BLK_T_IN
			if sqe.Opcode == iouring.IOUringOpWrite {
				requestType = block.VIRTIO_BLK_T_OUT
				asm.CleanDCacheRange(kernelVA, uintptr(totalBytes))
				asm.DmaWmb()
			} else {
				asm.InvalidateDCacheRange(kernelVA, uintptr(totalBytes))
				asm.DmaWmb()
			}

			// Submit to VirtIO engine.
			tag, err := dev.DoBlockIOSubmit(uint32(requestType), sqe.Off, nil, 0, pa, uint32(totalBytes))
			if err != nil {
				serial.RawUARTPuts("[IOUring] submit failed\r\n")
				return -5 // EIO
			}

			// Store per-tag metadata for IRQ top-half.
			sidecarSlot := dev.GetInFlightSidecar(0)
			dataKernelVA := pa + constants.KernelMMIOOffset
			if sqe.Opcode == iouring.IOUringOpWrite {
				dataKernelVA = 0
			}
			setBlockAsyncSlot(tag, sidecarSlot.VA+16, sidecarSlot.Index,
				dataKernelVA, uint32(totalBytes), uintptr(unsafe.Pointer(clump)),
				sqe.UserData)

			// Notify device after each submit.
			asm.Dsb()
			dev.Eng.Notify()
			submitted++
		}

		// Advance SQ head.
		atomic.StoreUint32(&ring.SQHead, sqHead+toSubmit)
	}

	// Phase B: Wait for completions.
	if minComplete > 0 {
		// Fast path: check if completions are already available.
		cqTail := atomic.LoadUint32(&ring.CQTail)
		cqHead := atomic.LoadUint32(&ring.CQHead)
		completions := cqTail - cqHead

		if completions >= minComplete {
			return int64(completions)
		}

		// Block: find next thread, context-switch.
		ctxPtr := blockForIOUring(ringID, minComplete, uint64(mazzy.SysIOUringEnter))

		if ctxPtr == ^uintptr(0) {
			// Sentinel: completions arrived between fast-path check and
			// IRQ disable. Return them directly.
			cqTail = atomic.LoadUint32(&ring.CQTail)
			cqHead = atomic.LoadUint32(&ring.CQHead)
			return int64(cqTail - cqHead)
		}
		if ctxPtr != 0 {
			SetSyscallSwitchTarget(ctxPtr)
			return -11 // Overwritten by SVC re-execution on wake
		}

		// WFI fallback: no other thread to run, spin-wait with WFI.
		for {
			enableIRQsAndWait()
			cqTail = atomic.LoadUint32(&ring.CQTail)
			cqHead = atomic.LoadUint32(&ring.CQHead)
			completions = cqTail - cqHead
			if completions >= minComplete {
				return int64(completions)
			}
			// Check timeout in WFI loop too.
			if checkIOUringWFITimeout(ringID, cqTail-cqHead) {
				return int64(cqTail - cqHead)
			}
		}
	}

	return int64(submitted)
}

// Bridge functions — reach kmazarin/kmazarin (main package) via go:linkname.

//go:linkname blockForIOUring main.BlockForIOUring
func blockForIOUring(ringID int, minComplete uint32, syscallNum uint64) uintptr

//go:linkname initIOUringTimeout main.InitIOUringTimeout
func initIOUringTimeout()

//go:linkname maxIORings main.MaxIORingsFunc
func maxIORings() int

//go:linkname ioUringSlotKVA main.IOUringSlotKVA
func ioUringSlotKVA(ringID int) uintptr

//go:linkname ioUringSlotOwnerSID main.IOUringSlotOwnerSID
func ioUringSlotOwnerSID(ringID int) int16

//go:linkname getIOUringSlot main.GetIOUringSlot
func getIOUringSlot(ringID int) unsafe.Pointer

//go:linkname setupIOUringSlot main.SetupIOUringSlot
func setupIOUringSlot(ringID int, kva, pa uintptr, ownerSID int16, deviceType uint8)

//go:linkname checkIOUringWFITimeout main.CheckIOUringWFITimeout
func checkIOUringWFITimeout(ringID int, currentCompletions uint32) bool

//go:linkname ioUringSlotDeviceType main.IOUringSlotDeviceType
func ioUringSlotDeviceType(ringID int) uint8
