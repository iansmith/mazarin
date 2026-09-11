package ksyscall

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
)

// MAZ-203 duplicate-delivery probes. The delegate reply gate (MAZ-201) caught
// the linux shepherd replying a second time to a batch of already-answered
// delegates; the shepherd retains no such requests, so the duplicates must
// have been delivered twice by the ring layer. These counters (surfaced on
// the [status] line) and the paired [URING:*] Criticalf lines discriminate
// where: a head anomaly means the consumer path retired a ring index it had
// already retired (re-delivery of consumed entries); a concurrent-recv hit
// means two kernel contexts ran the single-consumer drain path for one ring
// at once — the double-run precondition for a head rollback.

// UringHeadAnomalies counts advanceUringHead calls that retired an index the
// consume-order shadow did not expect.
var UringHeadAnomalies atomic.Uint64

// UringConcurrentRecv counts entries to a ring's drain path while another
// context already held its single-consumer claim.
var UringConcurrentRecv atomic.Uint64

// uringRecvActive is the per-ring single-consumer claim used by the
// concurrent-recv probe. Claimed only around drain+advance, never across a
// block or WFI wait. The claim is DIAGNOSTIC ONLY: a failed claim is
// counted and logged but does not block the drain — concurrent consumption
// is observed, not prevented (the drainedHead handoff to advanceUringHead
// is what makes the race visible as a head anomaly).
var uringRecvActive [proc.MaxLiveShepherds][ipc.MaxRingsPerShepherd]uint32

// claimRecv attempts the single-consumer claim; a failed claim is the
// anomaly and is logged and counted. Returns whether THIS caller holds the
// claim (and must release it).
func claimRecv(sid int16, ringIdx int) bool {
	if atomic.CompareAndSwapUint32(&uringRecvActive[sid][ringIdx], 0, 1) {
		return true
	}
	UringConcurrentRecv.Add(1)
	klog.Criticalf("[URING]", "[URING:concurrent-recv] sid=%d ring=%d\n",
		int32(sid), int32(ringIdx))
	return false
}

// releaseRecv drops the claim taken by claimRecv.
func releaseRecv(sid int16, ringIdx int) {
	atomic.StoreUint32(&uringRecvActive[sid][ringIdx], 0)
}

// tryDrainOnce runs one claim+drain+advance+release cycle for the ring and,
// on a successful drain, copies the message out, reports any consume-order
// anomaly, and wakes a sender parked on the just-freed slot. Returns
// (copy result, true) when a message was consumed, (0, false) on empty.
// The MAZ-203 single-consumer claim brackets only drain+advance — never a
// block or WFI wait.
func tryDrainOnce(sid int16, ringIdx int, bufPtr uint64) (int64, bool) {
	claimed := claimRecv(sid, ringIdx)
	msgKVA, drainedHead, ok := drainUringIPCRing(sid, ringIdx)
	if !ok {
		if claimed {
			releaseRecv(sid, ringIdx)
		}
		return 0, false
	}
	result := copyUringMsgToUser(bufPtr, msgKVA)
	// Hand the drain's OWN head index to the consume-order check — a live
	// re-read could never see a concurrent double-drain of the same slot.
	anomaly := advanceUringHead(sid, ringIdx, drainedHead)
	if claimed {
		releaseRecv(sid, ringIdx)
	}
	reportHeadAnomaly(sid, ringIdx, anomaly)
	wakeSenderAfterDrain(sid, ringIdx)
	return result, true
}

// reportHeadAnomaly logs a nonzero advanceUringHead anomaly (packed
// head<<32|expected) with the ring identity.
func reportHeadAnomaly(sid int16, ringIdx int, anomaly uint64) {
	if anomaly == 0 {
		return
	}
	UringHeadAnomalies.Add(1)
	klog.Criticalf("[URING]", "[URING:head-anomaly] sid=%d ring=%d head=%d expected=%d\n",
		int32(sid), int32(ringIdx), uint32(anomaly>>32), uint32(anomaly))
}

// SyscallUringConnect connects to a target shepherd's IPC uring by uring ID.
// arg0 = target uring ID (uint64)
// arg1 = target ring index (0 = default, 1-2 = additional rings)
// Returns: connection handle (small integer) on success, or negative errno.
//
// This syscall routes through KernelSVCWorker because the uring ID map lookup
// may need heap access (runs on thread 0's growable stack).
//
//go:noinline
func SyscallUringConnect(arg0, arg1, _, _, _, _ uint64) int64 {
	targetUringID := arg0
	ringIdx := uint8(arg1)
	if targetUringID == 0 {
		return -22 // EINVAL — 0 is not a valid uring ID
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	req := uringConnectWorkRequest{
		TargetUringID: targetUringID,
		CallerSID:     int16(shepherd.PID),
		TargetRingIdx: ringIdx,
	}

	ctxPtr := submitUringConnect(req)
	if ctxPtr == 0 {
		return -16 // EBUSY — worker busy or no thread to switch to
	}
	SetSyscallSwitchTarget(ctxPtr)
	return 0 // overwritten by wakeBlockedThread
}

// SyscallUringSend sends a 128-byte message to a target shepherd's uring ring.
// arg0 = target SID (int16)
// arg1 = pointer to 128-byte message in caller's address space
// arg2 = target ring index (0 = default, 1-2 = additional rings)
// Returns: 0 on success, negative errno on failure.
//
//go:noinline
func SyscallUringSend(arg0, arg1, arg2, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	msgPtr := uintptr(arg1)
	ringIdx := uint8(arg2)

	if msgPtr == 0 {
		return -14 // EFAULT
	}

	callerSID := getCurrentThreadSID()

	// Resolve the user VA to a kernel-accessible address.
	pageOffset := msgPtr & (kmem.PageSize - 1)
	var msgKVA uintptr

	if pageOffset+uintptr(ipc.UringIPCSlotSize) <= kmem.PageSize {
		// Fast path: message fits within one page.
		userPA := kmem.WalkUserPageTable(msgPtr)
		if userPA == 0 {
			if !kmem.HandleUserPageFault(msgPtr, 0) {
				return -14 // EFAULT
			}
			userPA = kmem.WalkUserPageTable(msgPtr)
			if userPA == 0 {
				return -14 // EFAULT
			}
		}
		scratchVA := kmem.MapPAToKernelScratch(userPA &^ (kmem.PageSize - 1))
		if scratchVA == 0 {
			return -14 // EFAULT
		}
		msgKVA = scratchVA + uintptr(pageOffset)
	} else {
		// Slow path: message spans a page boundary — copy to a local buffer.
		// Happens when a 128-byte stack variable lands in the last 128 bytes of
		// a page (platform-dependent stack layout, seen on x86_64).
		var localBuf [ipc.UringIPCSlotSize]byte
		firstBytes := kmem.PageSize - pageOffset

		userPA1 := kmem.WalkUserPageTable(msgPtr)
		if userPA1 == 0 {
			if !kmem.HandleUserPageFault(msgPtr, 0) {
				return -14
			}
			userPA1 = kmem.WalkUserPageTable(msgPtr)
			if userPA1 == 0 {
				return -14
			}
		}
		scratchVA1 := kmem.MapPAToKernelScratch(userPA1 &^ (kmem.PageSize - 1))
		if scratchVA1 == 0 {
			return -14
		}
		copy(localBuf[:firstBytes],
			unsafe.Slice((*byte)(unsafe.Pointer(scratchVA1+pageOffset)), int(firstBytes)))

		secondPageVA := (msgPtr &^ (kmem.PageSize - 1)) + kmem.PageSize
		userPA2 := kmem.WalkUserPageTable(secondPageVA)
		if userPA2 == 0 {
			if !kmem.HandleUserPageFault(secondPageVA, 0) {
				return -14
			}
			userPA2 = kmem.WalkUserPageTable(secondPageVA)
			if userPA2 == 0 {
				return -14
			}
		}
		scratchVA2 := kmem.MapPAToKernelScratch(userPA2 &^ (kmem.PageSize - 1))
		if scratchVA2 == 0 {
			return -14
		}
		remaining := uintptr(ipc.UringIPCSlotSize) - firstBytes
		copy(localBuf[firstBytes:],
			unsafe.Slice((*byte)(unsafe.Pointer(scratchVA2)), int(remaining)))

		msgKVA = uintptr(unsafe.Pointer(&localBuf[0]))
	}

	// Stamp sender fields into the message before writing to ring
	msg := (*ipc.UringIPCMsg)(unsafe.Pointer(msgKVA))
	msg.SenderSID = callerSID
	shepherd := proc.CurrentShepherd()
	if shepherd != nil {
		msg.SenderID = shepherd.UringID
	}

	result, ctxPtr := uringSendKernel(callerSID, targetSID, ringIdx, msgKVA)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	}
	return result
}

// SyscallUringRecv blocks until a message arrives on the caller's IPC uring ring.
// arg0 = pointer to 128-byte buffer in caller's address space
// arg1 = ring index (0 = default, 1-2 = additional rings)
// Returns: 0 on success (message written to buf), negative errno on failure.
//
//go:noinline
func SyscallUringRecv(arg0, arg1, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	ringIdx := int(arg1)
	if bufPtr == 0 {
		return -14 // EFAULT
	}
	// Validate the untrusted ring index (and the sid) HERE, before any
	// kernel array is touched: tryDrainOnce's single-consumer claim indexes
	// uringRecvActive[sid][ringIdx] ahead of drainUringIPCRing's own bounds
	// check, so relying on the downstream check would let a bad arg1 panic
	// the kernel on the array bounds.
	if ringIdx < 0 || ringIdx >= ipc.MaxRingsPerShepherd {
		return -22 // EINVAL
	}

	sid := getCurrentThreadSID()
	if sid < 0 || int(sid) >= proc.MaxLiveShepherds {
		// Corrupted/out-of-range SID (a nil current thread reads as SID 0,
		// which is in-bounds and NOT caught here). Pre-probe, such a SID
		// fell through drain's bounds check into BlockForUringRecv, which
		// would mark the thread ThreadBlockedUringRecv without wiring
		// BlockedTID — parked forever, unwakeable. Fail loudly instead.
		return -1 // EPERM
	}
	shepherdIdx := int(sid)

	// Try to drain immediately. On success this also wakes any sender
	// parked on ThreadBlockedUringSend for this ring (Scenario B drain wake).
	if result, ok := tryDrainOnce(sid, ringIdx, bufPtr); ok {
		return result
	}

	// Block until message arrives
	ctxPtr := blockForUringRecv(shepherdIdx, ringIdx, bufPtr)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value overwritten by re-executed SVC on wake
	}

	// No other thread — WFI loop
	for {
		enableIRQsAndWait()
		if result, ok := tryDrainOnce(sid, ringIdx, bufPtr); ok {
			return result
		}
	}
}

// SyscallUringRelease releases a connection to a target shepherd's uring ring.
// arg0 = connection handle (from SysUringConnect)
// Returns: 0 on success, negative errno on failure.
//
//go:noinline
func SyscallUringRelease(arg0, _, _, _, _, _ uint64) int64 {
	handle := int(arg0)

	callerSID := getCurrentThreadSID()

	return releaseUringConnection(handle, callerSID)
}

// SyscallUringSetup creates an additional uring ring (ring 1 or 2) for the
// calling shepherd. Ring 0 is always created at shepherd startup.
// arg0 = ring index to create (1 or 2)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallUringSetup(arg0, _, _, _, _, _ uint64) int64 {
	ringIdx := int(arg0)
	if ringIdx < 1 || ringIdx >= ipc.MaxRingsPerShepherd {
		return -22 // EINVAL — ring 0 is auto-created; only 1 or 2 allowed
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	if !allocateUringIPCRing(shepherd, ringIdx) {
		return -12 // ENOMEM
	}

	return 0
}

// copyUringMsgToUser copies a 128-byte message from kernel ring slot to userspace.
func copyUringMsgToUser(bufPtr uint64, msgKVA uintptr) int64 {
	// Resolve user buffer to kernel-accessible address
	pageOffset := uintptr(bufPtr) & (kmem.PageSize - 1)
	if pageOffset+uintptr(ipc.UringIPCSlotSize) > kmem.PageSize {
		return -22 // EINVAL — buffer spans page boundary
	}

	userPA := kmem.WalkUserPageTable(uintptr(bufPtr))
	if userPA == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr), 0) {
			return -14 // EFAULT
		}
		userPA = kmem.WalkUserPageTable(uintptr(bufPtr))
		if userPA == 0 {
			return -14 // EFAULT
		}
	}

	scratchVA := kmem.MapPAToKernelScratch(userPA &^ (kmem.PageSize - 1))
	if scratchVA == 0 {
		return -14 // EFAULT
	}

	dstKVA := scratchVA + pageOffset

	// Copy 128 bytes
	src := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(msgKVA))
	dst := (*[ipc.UringIPCSlotSize]byte)(unsafe.Pointer(dstKVA))
	*dst = *src

	return 0
}
