package main

// notify_bridge.go — Bridge between ksyscall constraint notification and the scheduler.
//
// BlockForDirtyNotify blocks the current thread waiting for dirty notifications.
// WakeDirtyNotifyThread wakes a blocked thread when notifications arrive.

import (
	"mazzy/kmazarin/asm"
	"unsafe"
)

// BlockForDirtyNotify blocks the current thread waiting for dirty notifications.
// Saves syscall arg0 for rewind restoration.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForDirtyNotify(syscallArg0 uint64) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Find next thread to run.
	var next *Thread
	if t.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		processStaticDeadlinesSchedLockHeld()
		if t.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && t.PageTableL0PA != 0 {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Block current thread.
	t.State = ThreadBlockedDirtyNotify
	t.SoftIRQSlotArg = syscallArg0    // Save arg0 for rewind
	t.SoftIRQSyscallNum = 0x102A      // SysAttrWaitDirty — for x86_64 RAX restore

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
