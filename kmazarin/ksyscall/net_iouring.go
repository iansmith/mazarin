package ksyscall

// net_iouring.go — SQE dispatch for net rings (MAZ-28 step 2).
//
// Net.elf's io_uring ring carries only IOUringOpNetRearmDesc SQEs in
// step 2. The handler runs in full Go context (not nosplit) under the
// SyscallIOUringEnter SVC path, so descriptor mutation and cache
// management have a full goroutine stack to work with.
//
// Slot-pinned descriptor model: each RX descIdx is permanently "owned"
// by net.elf. PopUsedNoFree in the IRQ branch pops the used ring
// without returning the descriptor to the free list. Re-arming writes
// directly to desc[descIdx] and adds to the available ring, bypassing
// Engine.Submit's free-list bookkeeping entirely. Free-list state for
// descs 0..N-1 is irrelevant in step 2 because nothing else operates
// on RxEng.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	netdev "mazzy/kmazarin/device/virtio/net"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"mazzy/shared/constants"
	"mazzy/shared/iouring"
	"unsafe"
)

// dispatchNetSQEs processes net-ring SQEs. Handles both
// IOUringOpNetRearmDesc (RX re-arm, slot-pinned) and IOUringOpNetSubmitTx
// (TX submit, fresh chain each time).
//
// Returns (submittedCount, errno). errno != 0 means abort;
// submittedCount may be partial.
func dispatchNetSQEs(shepherd *proc.Shepherd, ring *iouring.IORing, sqHead, toSubmit uint32) (uint32, int64) {
	dev := netdev.GetDevice()
	if dev == nil || dev.IRQNum == 0 {
		return 0, -19 // ENODEV
	}

	submitted := uint32(0)
	rxNotify := false
	txNotify := false

	for i := uint32(0); i < toSubmit; i++ {
		idx := (sqHead + i) & iouring.SQMask
		sqe := &ring.SQEntries[idx]

		switch sqe.Opcode {
		case iouring.IOUringOpNop:
			submitted++
			continue

		case iouring.IOUringOpNetRearmDesc:
			if err := handleNetRearmDesc(shepherd, dev, sqe); err != 0 {
				return submitted, err
			}
			rxNotify = true
			submitted++

		case iouring.IOUringOpNetSubmitTx:
			if err := handleNetSubmitTx(shepherd, dev, sqe); err != 0 {
				return submitted, err
			}
			txNotify = true
			submitted++

		default:
			klog.Errf("[IOUring net] EINVAL: unknown opcode %d\n", uint64(sqe.Opcode))
			return submitted, -22 // EINVAL
		}
	}

	if rxNotify {
		dev.RxEng.Notify()
	}
	if txNotify {
		dev.TxEng.Notify()
	}
	return submitted, 0
}

// handleNetRearmDesc writes a fresh page PA into desc[descIdx] and adds
// it to the available ring. Slot-pinned — bypasses the free list.
func handleNetRearmDesc(shepherd *proc.Shepherd, dev *netdev.VirtIONetDevice, sqe *iouring.SQEntry) int64 {
	vq := &dev.RxEng.VQ
	descStride := unsafe.Sizeof(virtio.VirtQDesc{})

	descIdx := uint16(sqe.FD)
	if int(descIdx) >= int(vq.QueueSize) {
		klog.Errf("[IOUring net] EINVAL: descIdx %d out of range\n", uint64(descIdx))
		return -22 // EINVAL
	}

	pageVA := uintptr(sqe.Addr)
	// Slot-pinned re-arm publishes a full 4 KiB descriptor; refuse a
	// non-page-aligned base so the device can't DMA 4 KiB starting
	// mid-page into adjacent physical memory.
	if pageVA&0xFFF != 0 {
		klog.Errf("[IOUring net] EINVAL: rearm VA 0x%x not page-aligned\n", uint64(pageVA))
		return -22 // EINVAL
	}
	clump := shepherd.FindClumpByVA(pageVA)
	if clump == nil {
		klog.Errf("[IOUring net] EFAULT: rearm VA 0x%x not in clump\n", uint64(pageVA))
		return -14 // EFAULT
	}
	pagePA := clump.LookupPA(pageVA)
	if pagePA == 0 {
		klog.Errf("[IOUring net] EFAULT: rearm VA 0x%x has no PA\n", uint64(pageVA))
		return -14 // EFAULT
	}

	desc := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(descIdx)*descStride))
	desc.Addr = uint64(pagePA)
	desc.Len = 4096
	desc.Flags = virtio.VIRTQ_DESC_F_WRITE
	desc.Next = 0xFFFF

	asm.CleanDCacheRange(uintptr(unsafe.Pointer(desc)), descStride)
	asm.Dsb()

	virtio.VirtqueueAddToAvailable(vq, descIdx)
	return 0
}

// handleNetSubmitTx builds a 1-descriptor TX chain pointing at the
// caller's page (slice (pageVA+offset, len)) and submits it on TxEng.
// Records the caller's txTag in dev.TxTagByDescIdx so the IRQ branch
// can encode it in the TX completion CQE. Serialized by dev.TxSubmitMu.
func handleNetSubmitTx(shepherd *proc.Shepherd, dev *netdev.VirtIONetDevice, sqe *iouring.SQEntry) int64 {
	pageVA := uintptr(sqe.Addr)
	offset := uintptr(sqe.Off)
	length := sqe.Len
	txTag := uint16(sqe.UserData)

	// The descriptor PA we publish is pageVA→PA + offset; if pageVA
	// isn't page-aligned, the PA lookup below resolves only the first
	// byte and the device can read past the intended page once Off
	// is applied. Same hazard as the RX rearm path — refuse the SQE.
	if pageVA&0xFFF != 0 {
		klog.Errf("[IOUring net] EINVAL: TX VA 0x%x not page-aligned\n", uint64(pageVA))
		return -22 // EINVAL
	}
	if length == 0 || length > 4096 || offset+uintptr(length) > 4096 {
		klog.Errf("[IOUring net] EINVAL: bad TX length=%d offset=%d\n", uint64(length), uint64(offset))
		return -22 // EINVAL
	}

	clump := shepherd.FindClumpByVA(pageVA)
	if clump == nil {
		klog.Errf("[IOUring net] EFAULT: TX VA 0x%x not in clump\n", uint64(pageVA))
		return -14 // EFAULT
	}
	pagePA := clump.LookupPA(pageVA)
	if pagePA == 0 {
		klog.Errf("[IOUring net] EFAULT: TX VA 0x%x has no PA\n", uint64(pageVA))
		return -14 // EFAULT
	}

	// Frame buffer is cacheable userspace memory; clean dirty CPU cache
	// lines so the device DMAs out what the CPU just wrote.
	frameKernelVA := pagePA + offset + constants.KernelVAOffset
	asm.CleanDCacheRange(frameKernelVA, uintptr(length))
	asm.DmaWmb()

	dev.TxSubmitMu.Lock()
	defer dev.TxSubmitMu.Unlock()

	var chain virtio.DescChain
	chain.Count = 1
	chain.Descs[0] = virtio.DescSpec{
		PA:    uint64(pagePA) + uint64(offset),
		Len:   length,
		Flags: 0, // device-readable
	}

	tag := dev.TxEng.Submit(&chain)
	if tag == virtio.InvalidIOTag {
		klog.Errf("[IOUring net] TX submit failed (no free descs)\n")
		return -5 // EIO
	}

	descIdx := uint16(tag)
	if int(descIdx) < len(dev.TxTagByDescIdx) {
		dev.TxTagByDescIdx[descIdx] = txTag
	}
	return 0
}

// SyscallNetReadRxLatencyUs returns the IRQ→now latency in microseconds
// for the given RX descriptor tag (MAZ-28 step 2). Net.elf calls this
// right after dequeuing a CQE to compute IRQ→shepherd latency.
//
// arg0 = tag (RX descIdx, 0..127)
//
// Returns: latency in µs (0 if tag out of range or timestamp not yet
// recorded). The race window with concurrent IRQs on the same slot is
// accepted as documented in RxIRQTimestamps; for step 2's one-ARP-frame
// test this can't hit.
//
//go:noinline
func SyscallNetReadRxLatencyUs(arg0, _, _, _, _, _ uint64) int64 {
	tag := uint16(arg0)
	irqTs := netdev.ReadRxIRQTimestamp(tag)
	if irqTs == 0 {
		return 0
	}
	now := kirq.ReadCounterValue()
	freq := kirq.SystemTimerFrequency
	if freq == 0 || now <= irqTs {
		return 0
	}
	deltaTicks := now - irqTs
	// µs = ticks * 1e6 / freq. Multiply first; deltaTicks is small (sub-ms).
	deltaUs := (deltaTicks * 1_000_000) / freq
	if deltaUs > 0x7FFFFFFF {
		return 0x7FFFFFFF
	}
	return int64(deltaUs)
}
