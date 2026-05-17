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
	"mazzy/shared/iouring"
	"unsafe"
)

// dispatchNetSQEs processes net-ring SQEs. Returns (submittedCount, errno).
// errno != 0 means abort; submittedCount may be partial.
func dispatchNetSQEs(shepherd *proc.Shepherd, ring *iouring.IORing, sqHead, toSubmit uint32) (uint32, int64) {
	dev := netdev.GetDevice()
	if dev == nil || dev.IRQNum == 0 {
		return 0, -19 // ENODEV
	}
	vq := &dev.RxEng.VQ
	descStride := unsafe.Sizeof(virtio.VirtQDesc{})

	submitted := uint32(0)
	notified := false

	for i := uint32(0); i < toSubmit; i++ {
		idx := (sqHead + i) & iouring.SQMask
		sqe := &ring.SQEntries[idx]

		if sqe.Opcode == iouring.IOUringOpNop {
			submitted++
			continue
		}
		if sqe.Opcode != iouring.IOUringOpNetRearmDesc {
			klog.Errf("[IOUring net] EINVAL: unknown opcode %d\n", uint64(sqe.Opcode))
			return submitted, -22 // EINVAL
		}

		descIdx := uint16(sqe.FD)
		if int(descIdx) >= int(vq.QueueSize) {
			klog.Errf("[IOUring net] EINVAL: descIdx %d out of range\n", uint64(descIdx))
			return submitted, -22 // EINVAL
		}

		// Translate userspace VA → PA via the shepherd's DMA clump map.
		// Net.elf allocates the RX pool with mem.AllocContiguous, so
		// every pageVA passed here lives in one of its clumps.
		pageVA := uintptr(sqe.Addr)
		clump := shepherd.FindClumpByVA(pageVA)
		if clump == nil {
			klog.Errf("[IOUring net] EFAULT: VA 0x%x not in clump\n", uint64(pageVA))
			return submitted, -14 // EFAULT
		}
		pagePA := clump.LookupPA(pageVA)
		if pagePA == 0 {
			klog.Errf("[IOUring net] EFAULT: VA 0x%x has no PA\n", uint64(pageVA))
			return submitted, -14 // EFAULT
		}

		// Write descriptor in place (slot-pinned — no free-list churn).
		desc := (*virtio.VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(descIdx)*descStride))
		desc.Addr = uint64(pagePA)
		desc.Len = 4096 // Full page available to one RX frame.
		desc.Flags = virtio.VIRTQ_DESC_F_WRITE
		desc.Next = 0xFFFF

		// Clean the descriptor's cache line so the device sees it
		// (matches Engine.Submit's per-descriptor clean).
		asm.CleanDCacheRange(uintptr(unsafe.Pointer(desc)), descStride)
		asm.Dsb()

		// Add to available ring. VirtqueueAddToAvailable does its own
		// MMIO write to the device's avail-idx MMIO mirror, but we
		// batch the device kick — Notify once after the loop.
		virtio.VirtqueueAddToAvailable(vq, descIdx)
		notified = true

		submitted++
	}

	if notified {
		dev.RxEng.Notify()
	}
	return submitted, 0
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
