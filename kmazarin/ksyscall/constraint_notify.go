// constraint_notify.go — Per-shepherd dirty notification queue and WaitDirty syscall.
//
// When a value attribute is written and the dirty walk encounters a node with
// FlagEagerNotify, the kernel enqueues the slot number in the owning shepherd's
// notification queue. A shepherd blocked on SysAttrWaitDirty is woken to drain
// the queue.
//
// SysAttrSetEager (0x1029) — set/clear FlagEagerNotify on a shared-page node.
// SysAttrWaitDirty (0x102A) — block until dirty notifications arrive.

package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/mazarin/vm/flat"
	"unsafe"
)

// notifyQueueSize is the maximum number of pending dirty slot notifications
// per shepherd before overflow coalescing kicks in.
const notifyQueueSize = 64

// NotifyQueue holds pending dirty slot notifications for a shepherd.
// Fixed-size ring buffer — if full, set Overflowed (shepherd will re-scan).
type NotifyQueue struct {
	Slots      [notifyQueueSize]uint16
	Head       uint16 // next write position (wraps at notifyQueueSize)
	Count      uint16 // number of pending notifications
	Overflowed bool   // if true, shepherd must re-scan all eager attrs
	BlockedTID int32  // TID of thread blocked on WaitDirty (-1 = none)
}

// notifyQueues is the per-shepherd notification queue array.
// Indexed by ShepherdId (0 to MaxShepherds-1).
var notifyQueues [proc.MaxShepherds]NotifyQueue

// initNotifyQueues initializes all notification queues.
// Called from InitKernelAttrManager.
func initNotifyQueues() {
	for i := range notifyQueues {
		notifyQueues[i].BlockedTID = -1
	}
}

// enqueueNotification records a dirty slot for a shepherd and wakes its blocked
// WaitDirty thread (if any). Called from dirtyWalk when FlagEagerNotify is set.
//
// The owner field comes from FlatAttrNode.Owner (uint16, which stores the
// ShepherdId cast to uint16).
func (mgr *KernelAttrManager) enqueueNotification(slot uint16, owner uint16) {
	pid := int(owner)
	if pid < 0 || pid >= proc.MaxShepherds {
		return
	}
	q := &notifyQueues[pid]

	if q.Count >= notifyQueueSize {
		q.Overflowed = true
	} else {
		// Dedup: don't enqueue the same slot twice.
		for i := uint16(0); i < q.Count; i++ {
			idx := (q.Head - q.Count + i) % notifyQueueSize
			if q.Slots[idx] == slot {
				return // already pending
			}
		}
		q.Slots[q.Head] = slot
		q.Head = (q.Head + 1) % notifyQueueSize
		q.Count++
	}

	// Wake the blocked thread if any.
	if q.BlockedTID >= 0 {
		tid := q.BlockedTID
		q.BlockedTID = -1
		wakeDirtyNotifyThread(tid)
	}
}

// drainNotifyQueue copies pending slot numbers to a kernel buffer and resets
// the queue. Returns the count of slots drained, or -1 if overflow occurred.
func drainNotifyQueue(pid int) ([]uint16, int) {
	if pid < 0 || pid >= proc.MaxShepherds {
		return nil, 0
	}
	q := &notifyQueues[pid]

	if q.Overflowed {
		q.Count = 0
		q.Head = 0
		q.Overflowed = false
		return nil, -1 // overflow signal
	}

	if q.Count == 0 {
		return nil, 0
	}

	count := int(q.Count)
	var result [notifyQueueSize]uint16
	// Read from oldest to newest.
	start := (q.Head - q.Count) % notifyQueueSize
	for i := 0; i < count; i++ {
		idx := (start + uint16(i)) % notifyQueueSize
		result[i] = q.Slots[idx]
	}

	q.Count = 0
	q.Head = 0

	return result[:count], count
}

// SyscallAttrSetEager sets or clears FlagEagerNotify on an attribute node.
//
// Args: slotIndex, eager (0=clear, 1=set), _, _, _, _
// Returns: 0 on success, negative errno on failure.
func SyscallAttrSetEager(slotIndex, eager, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12 // ENOMEM
	}

	slot := uint16(slotIndex)
	if !attrMgr.isNodeAllocated(slot) {
		return -22 // EINVAL
	}

	node := attrMgr.node(slot)
	if node.IsTombstoned() {
		return -22 // EINVAL
	}

	// Check ownership.
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	if eager != 0 {
		node.Flags |= flat.FlagEagerNotify
	} else {
		node.Flags &^= flat.FlagEagerNotify
	}

	return 0
}

// SyscallAttrWaitDirty blocks until dirty notifications are available for the
// calling shepherd, then copies slot numbers to userspace.
//
// Args: resultBufPtr, maxSlots, _, _, _, _
//   - resultBufPtr: pointer to uint16 array in userspace
//   - maxSlots: maximum number of slots to return
//
// Returns: count of dirty slots (>0), -1 on overflow (re-scan all), -EAGAIN for retry.
func SyscallAttrWaitDirty(resultBufPtr, maxSlots, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12 // ENOMEM
	}

	pid, _ := getCurrentThreadSIDAndTID()
	shepherdSID := int(pid)
	if shepherdSID < 0 || shepherdSID >= proc.MaxShepherds {
		return -22 // EINVAL
	}

	if resultBufPtr == 0 || maxSlots == 0 {
		return -22 // EINVAL
	}

	// Try to drain pending notifications.
	slots, count := drainNotifyQueue(shepherdSID)
	if count == -1 {
		return -1 // overflow — caller must re-scan all eager attrs
	}
	if count > 0 {
		return writeSlotsToBuf(resultBufPtr, maxSlots, slots, count)
	}

	// No notifications pending — block the thread.
	q := &notifyQueues[shepherdSID]
	_, tid := getCurrentThreadSIDAndTID()
	q.BlockedTID = int32(tid)

	ctxPtr := blockForDirtyNotify(resultBufPtr)
	if ctxPtr != 0 {
		// Context switch to another thread. The wake path will rewind our
		// SVC so this syscall re-executes, finding items in the queue.
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value doesn't matter — overwritten by re-executed SVC
	}

	// No other thread to switch to — WFI loop until notifications arrive.
	for {
		enableIRQsAndWait()
		slots, count = drainNotifyQueue(shepherdSID)
		if count == -1 {
			return -1 // overflow
		}
		if count > 0 {
			return writeSlotsToBuf(resultBufPtr, maxSlots, slots, count)
		}
	}
}

// writeSlotsToBuf copies dirty slot numbers to userspace memory.
// Returns count of slots written, or negative errno on failure.
func writeSlotsToBuf(resultBufPtr, maxSlots uint64, slots []uint16, count int) int64 {
	n := count
	if n > int(maxSlots) {
		n = int(maxSlots)
	}

	// Ensure user page is mapped (demand-page if needed).
	if kmem.WalkUserPageTable(uintptr(resultBufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(resultBufPtr), 0) {
			return -14 // EFAULT
		}
	}

	userPA := kmem.WalkUserPageTable(uintptr(resultBufPtr))
	if userPA == 0 {
		return -14 // EFAULT
	}

	pageOffset := resultBufPtr & 0xFFF
	scratchVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
	if scratchVA == 0 {
		return -14 // EFAULT
	}

	dst := (*[notifyQueueSize]uint16)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	for i := 0; i < n; i++ {
		dst[i] = slots[i]
	}

	return int64(n)
}
