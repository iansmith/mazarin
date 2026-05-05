package main

// ipc_bridge.go — Bridge between ksyscall delegate/IPC handlers and the scheduler.
//
// The ksyscall package cannot directly access scheduler internals (thread states,
// ready queues). These functions are called from ksyscall via go:linkname to
// block/wake threads for delegate and IPC operations.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

// RecordDelegateBlock stamps the current thread with the sysID and current
// timer tick before it transitions to ThreadBlockedDelegate. Called from
// ksyscall.DelegateSyscall just before blockForDelegatedSyscall. printEpochStatus
// reads these fields for any thread still in ThreadBlockedDelegate so we can
// see exactly which delegated syscall is hung and for how long.
//
//go:nosplit
//go:noinline
func RecordDelegateBlock(sysID uint16) {
	t := GetCurrentThread()
	if t == nil {
		return
	}
	t.DelegateBlockSinceTick = kirq.ReadCounterValue()
	t.DelegateBlockSysID = sysID
}

// GetCurrentThreadPIDAndTID returns the PID and TID of the currently running thread.
//
//go:nosplit
//go:noinline
func GetCurrentThreadPIDAndTID() (proc.ShepherdId, int16) {
	t := GetCurrentThread()
	if t == nil {
		return -1, -1
	}
	return t.PID, int16(t.TID)
}

// BlockForDelegatedSyscall blocks the current thread (caller of a delegated syscall)
// waiting for the handler shepherd to reply.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForDelegatedSyscall() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedDelegate

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeDelegateCallerThread wakes the original caller blocked in ThreadBlockedDelegate.
// Called from SyscallReply when the handler sends the return value.
//
//go:nosplit
//go:noinline
func WakeDelegateCallerThread(pid int16, tid int32, returnVal int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.PID == proc.ShepherdId(pid) && t.State == ThreadBlockedDelegate {
		t.Context.SetReturnValue(uint64(returnVal))
		t.PreemptElapsed = 0
		t.DelegateBlockSinceTick = 0
		t.DelegateBlockSysID = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
		// MAZ-7: clear the delegate-stuck latch so a re-block on the
		// same TID can fire again.
		if int(tid) < threadArraySize {
			dbgDelegate10sLatch[tid] = 0
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// WakeDelegateCallerThreadNoReturn wakes a blocked delegate caller without
// modifying its saved registers. Used for MmapPageFill where the thread was
// blocked by a page fault — the CPU must retry the faulting instruction with
// the original register state intact (especially RAX).
//
//go:nosplit
//go:noinline
func WakeDelegateCallerThreadNoReturn(pid int16, tid int32) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.PID == proc.ShepherdId(pid) && t.State == ThreadBlockedDelegate {
		// Do NOT call SetReturnValue — preserve all saved registers
		t.PreemptElapsed = 0
		t.DelegateBlockSinceTick = 0
		t.DelegateBlockSysID = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
		if int(tid) < threadArraySize {
			dbgDelegate10sLatch[tid] = 0
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// BlockForWaitingIO blocks the current thread because the PL011 TX ring buffer
// is full. The thread's WaitingIO fields must be set before calling this.
// The TX interrupt top-half will drain from the thread's kernel buffer into the
// TX ring and wake the thread when the request is fully consumed.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForWaitingIO() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedWaitingIO
	// Save syscall args for RewindToSyscall restoration on wake.
	t.SoftIRQSlotArg = uint64(t.WaitingIOFd)
	t.SoftIRQSyscallNum = 0 // unused on ARM64
	waitingIOEnqueue(t)

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeWaitingIOThread wakes a thread blocked on WaitingIO after the TX
// interrupt top-half has fully consumed its kernel buffer.
// Called from the nosplit TX top-half with TxLock held and IRQs disabled.
//
//go:nosplit
//go:noinline
func WakeWaitingIOThread(t *Thread) {
	schedulerLock.Lock()

	if t.State != ThreadBlockedWaitingIO {
		schedulerLock.Unlock()
		return
	}

	atomic.StoreUint32(&t.WaitingIOComplete, 1)
	t.State = ThreadReady
	t.Context.RewindToSyscall()
	t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
	t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
	enqueueReadySchedLockHeld(t)
	asm.Dsb()

	schedulerLock.Unlock()
}

// CheckAndClearWaitingIO checks if the current thread was woken from WaitingIO.
// If so, clears the WaitingIO state and returns (true, userBytesWritten).
// Called from ksyscall via linkname on re-entry after RewindToSyscall.
//
//go:nosplit
func CheckAndClearWaitingIO() (bool, int) {
	t := GetCurrentThread()
	if t == nil {
		return false, 0
	}
	if atomic.LoadUint32(&t.WaitingIOComplete) == 0 {
		return false, 0
	}
	written := t.WaitingIOUserBytes
	atomic.StoreUint32(&t.WaitingIOComplete, 0)
	t.WaitingIOBuf = nil
	t.WaitingIOOffset = 0
	t.WaitingIOTotal = 0
	t.WaitingIOUserBytes = 0
	return true, written
}

// PrepareWaitingIO sets up the current thread's WaitingIO fields before blocking.
// Called from ksyscall via linkname.
//
//go:nosplit
func PrepareWaitingIO(buf []byte, total int, userBytes int, fd byte) {
	t := GetCurrentThread()
	if t == nil {
		return
	}
	t.WaitingIOBuf = buf
	t.WaitingIOOffset = 0
	t.WaitingIOTotal = total
	t.WaitingIOUserBytes = userBytes
	t.WaitingIOComplete = 0
	t.WaitingIOFd = fd
}
