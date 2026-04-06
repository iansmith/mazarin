package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
	"unsafe"
)

// SyscallUringConnect connects to a target shepherd's IPC uring by uring ID.
// arg0 = target uring ID (uint64)
// Returns: connection handle (small integer) on success, or negative errno.
//
// This syscall routes through KernelSVCWorker because the uring ID map lookup
// may need heap access (runs on thread 0's growable stack).
//
//go:noinline
func SyscallUringConnect(arg0, _, _, _, _, _ uint64) int64 {
	targetUringID := arg0
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
// Returns: 0 on success, negative errno on failure.
//
//go:noinline
func SyscallUringSend(arg0, arg1, _, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	msgPtr := uintptr(arg1)

	if msgPtr == 0 {
		return -14 // EFAULT
	}

	callerSID := getCurrentThreadSID()

	// Resolve the user VA to a kernel-accessible address.
	// The 128-byte message might span a page boundary, so we handle
	// the common case (within one page) and reject cross-page for now.
	pageOffset := msgPtr & (kmem.PageSize - 1)
	if pageOffset+uintptr(ipc.UringIPCSlotSize) > kmem.PageSize {
		return -22 // EINVAL — message spans page boundary
	}

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
	msgKVA := scratchVA + uintptr(pageOffset)

	// Stamp sender fields into the message before writing to ring
	msg := (*ipc.UringIPCMsg)(unsafe.Pointer(msgKVA))
	msg.SenderSID = callerSID
	shepherd := proc.CurrentShepherd()
	if shepherd != nil {
		msg.SenderID = shepherd.UringID
	}

	result, ctxPtr := uringSendKernel(callerSID, targetSID, msgKVA)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	}
	return result
}

// SyscallUringRecv blocks until a message arrives on the caller's IPC uring ring.
// arg0 = pointer to 128-byte buffer in caller's address space
// Returns: 0 on success (message written to buf), negative errno on failure.
//
//go:noinline
func SyscallUringRecv(arg0, _, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	sid := getCurrentThreadSID()
	shepherdIdx := int(sid)

	// Try to drain immediately
	msgKVA, ok := drainUringIPCRing(sid)
	if ok {
		result := copyUringMsgToUser(bufPtr, msgKVA)
		advanceUringHead(sid)
		return result
	}

	// Block until message arrives
	ctxPtr := blockForUringRecv(shepherdIdx, bufPtr)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value overwritten by re-executed SVC on wake
	}

	// No other thread — WFI loop
	for {
		enableIRQsAndWait()
		msgKVA, ok = drainUringIPCRing(sid)
		if ok {
			result := copyUringMsgToUser(bufPtr, msgKVA)
			advanceUringHead(sid)
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
