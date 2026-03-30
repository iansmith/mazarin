package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"unsafe"
)

// SyscallMailboxMapPage maps a page from the caller's address space into
// a target shepherd's address space and caches the VA↔VA translation.
// arg0 = targetSID (shepherd to map page into)
// arg1 = callerVA  (VA of the page/ring in caller's space — need not be page-aligned)
// Returns: target VA (with offset preserved) on success, or negative errno.
//
//go:noinline
func SyscallMailboxMapPage(arg0, arg1, _, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	callerVA := uintptr(arg1)

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	callerSID := int16(callerShepherd.PID)

	if callerSID == targetSID {
		return -22 // EINVAL — can't map into self
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetSID))
	if targetShepherd == nil || targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	pageOffset := callerVA & (kmem.PageSize - 1)
	callerPageVA := callerVA &^ (kmem.PageSize - 1)

	// Check if already cached
	if targetPageVA, ok := lookupVACache(callerSID, targetSID, callerPageVA); ok {
		return int64(targetPageVA + pageOffset)
	}

	// Resolve PA from caller's page table
	pa := kmem.WalkUserPageTableWithL0(callerPageVA, callerShepherd.PageTableL0PA)
	if pa == 0 {
		pa = kmem.DemandMapUserPage(callerPageVA, callerShepherd.PageTableL0PA)
		if pa == 0 {
			serial.RawUARTPuts("[Mailbox] page not mapped in caller\r\n")
			return -14 // EFAULT
		}
	}
	pa = pa &^ (kmem.PageSize - 1)

	// Verify ownership
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != callerSID {
		serial.RawUARTPuts("[Mailbox] page not owned by caller\r\n")
		return -1 // EPERM
	}

	// Increment refcount, mark shared
	desc.RefCount++
	desc.Flags |= kmem.PD_SHARED

	// Allocate VA in target
	targetPageVAu64 := bumpAllocForShepherd(targetShepherd, uint64(kmem.PageSize))
	if targetPageVAu64 == 0 {
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}
	targetPageVA := uintptr(targetPageVAu64)

	targetShepherd.Spans.Add(targetPageVAu64, uint64(kmem.PageSize))

	if !kmem.MapPageInProcess(targetSID, targetPageVA, pa, 0) {
		targetShepherd.Spans.Remove(targetPageVAu64, uint64(kmem.PageSize))
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	// Cache the translation
	addVACacheEntry(callerSID, targetSID, callerPageVA, targetPageVA)

	return int64(targetPageVA + pageOffset)
}

// SyscallMailboxSend sends a notification to a target shepherd.
// arg0 = targetSID
// arg1 = code (WMNotify or ShepherdNotify)
// arg2 = callerVA (ring buffer VA in caller's address space)
// Returns: 0 on success, or negative errno.
//
//go:noinline
func SyscallMailboxSend(arg0, arg1, arg2, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	code := int64(arg1)
	callerVA := uintptr(arg2)

	callerSID := getCurrentThreadSID()

	return mailboxSendKernel(int16(callerSID), targetSID, code, callerVA)
}

// SyscallMailboxRecv blocks until a mailbox notification arrives.
// arg0 = pointer to MailboxNotification struct in userspace
// Returns: 0 on success (notification written to buf), or negative errno.
//
//go:noinline
func SyscallMailboxRecv(arg0, _, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	sid := getCurrentThreadSID()
	shepherdIdx := int(sid)

	// Try to drain immediately
	notif, ok := drainMailboxQueue(shepherdIdx)
	if ok {
		return writeMailboxNotification(bufPtr, notif)
	}

	// Block until notification arrives
	ctxPtr := BlockForMailboxRecv(shepherdIdx, bufPtr)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value overwritten by re-executed SVC
	}

	// No other thread — WFI loop (should rarely be reached with thread 0 fallback)
	for {
		enableIRQsAndWait()
		notif, ok = drainMailboxQueue(shepherdIdx)
		if ok {
			return writeMailboxNotification(bufPtr, notif)
		}
	}
}

// writeMailboxNotification writes a notification struct to userspace.
func writeMailboxNotification(bufPtr uint64, notif mailboxNotification) int64 {
	if kmem.WalkUserPageTable(uintptr(bufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr), 0) {
			return -14
		}
	}

	userPA := kmem.WalkUserPageTable(uintptr(bufPtr))
	if userPA == 0 {
		return -14
	}

	pageOffset := bufPtr & 0xFFF
	scratchVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
	if scratchVA == 0 {
		return -14
	}

	type userNotif struct {
		Code      int64
		SenderSID int64
		RingAddr  uint64
	}

	dst := (*userNotif)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	dst.Code = notif.Code
	dst.SenderSID = int64(notif.SenderSID)
	dst.RingAddr = uint64(notif.RingAddr)

	return 0
}
