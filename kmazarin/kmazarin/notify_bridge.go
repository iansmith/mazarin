package main

// notify_bridge.go — Bridge between ksyscall constraint notification and the scheduler.
//
// BlockForDirtyNotify blocks the current thread waiting for dirty notifications.
// WakeDirtyNotifyThread wakes a blocked thread when notifications arrive.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/ksyscall"
	"unsafe"
)

// BlockForDirtyNotify blocks the current thread waiting for dirty notifications.
// Saves syscall arg0 for rewind restoration. Sets BlockedTID under the
// scheduler lock to avoid the race where a timer tick sees BlockedTID >= 0
// before the thread is actually blocked.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForDirtyNotify(syscallArg0 uint64, shepherdSID uint64) uintptr {
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
		// No other thread — set BlockedTID so the WFI loop path can be woken,
		// then return 0 to fall into the WFI loop in SyscallAttrWaitDirty.
		ksyscall.SetBlockedTID(int(shepherdSID), int32(t.TID))
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Block current thread. Set BlockedTID atomically with the state change
	// so timer ISR can't see BlockedTID before the thread is actually blocked.
	t.State = ThreadBlockedDirtyNotify
	t.SoftIRQSlotArg = syscallArg0    // Save arg0 for rewind
	t.SoftIRQSyscallNum = 0x102A      // SysAttrWaitDirty — for x86_64 RAX restore
	ksyscall.SetBlockedTID(int(shepherdSID), int32(t.TID))

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeDirtyNotifyThread wakes a thread blocked in ThreadBlockedDirtyNotify.
// Called from ksyscall's enqueueNotification when dirty notifications arrive.
//
//go:nosplit
//go:noinline
func WakeDirtyNotifyThread(tid int32) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedDirtyNotify {
		t.State = ThreadReady
		// Rewind so the SVC re-executes SysAttrWaitDirty, which will
		// find items in the notification queue and copy to userspace.
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		t.PreemptElapsed = 0
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// WakeDirtyNotifyThreadSchedLockHeld wakes a thread blocked in
// ThreadBlockedDirtyNotify. Same as WakeDirtyNotifyThread but the caller
// already holds schedulerLock with IRQs disabled (e.g. ProcessDeadlinesTopHalf).
//
//go:nosplit
//go:noinline
func WakeDirtyNotifyThreadSchedLockHeld(tid int32) {
	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedDirtyNotify {
		t.State = ThreadReady
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		t.PreemptElapsed = 0
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	}
}
