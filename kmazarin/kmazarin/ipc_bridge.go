package main

// ipc_bridge.go — Bridge between ksyscall delegate/IPC handlers and the scheduler.
//
// The ksyscall package cannot directly access scheduler internals (thread states,
// ready queues). These functions are called from ksyscall via go:linkname to
// block/wake threads for delegate and IPC operations.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"unsafe"
)

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

	t.State = ThreadBlockedDelegate

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// BlockForDelegatedRecv blocks the current thread (handler shepherd)
// waiting for a delegated syscall request.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForDelegatedRecv() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

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

	t.State = ThreadBlockedDelegateRecv

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeDelegateThread wakes a thread blocked in ThreadBlockedDelegateRecv,
// setting its return value.
//
//go:nosplit
//go:noinline
func WakeDelegateThread(tid int32, returnVal int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedDelegateRecv {
		t.Context.SetReturnValue(uint64(returnVal))
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()

	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// WakeDelegateCallerThread wakes the original caller blocked in ThreadBlockedDelegate.
// Called from SyscallReply when the handler sends the return value.
//
//go:noinline
func WakeDelegateCallerThread(pid int16, tid int32, returnVal int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t == nil {
		serial.RawUARTPuts("[WDCT] tid=")
		serial.RawUARTDecimal(uint64(tid))
		serial.RawUARTPuts(" NOT FOUND\r\n")
	} else if t.PID != proc.ShepherdId(pid) {
		serial.RawUARTPuts("[WDCT] tid=")
		serial.RawUARTDecimal(uint64(tid))
		serial.RawUARTPuts(" PID mismatch: want=")
		serial.RawUARTDecimal(uint64(pid))
		serial.RawUARTPuts(" got=")
		serial.RawUARTDecimal(uint64(t.PID))
		serial.RawUARTPuts("\r\n")
	} else if t.State != ThreadBlockedDelegate {
		serial.RawUARTPuts("[WDCT] tid=")
		serial.RawUARTDecimal(uint64(tid))
		serial.RawUARTPuts(" BAD STATE=")
		serial.RawUARTDecimal(uint64(t.State))
		serial.RawUARTPuts("\r\n")
	}
	if t != nil && t.PID == proc.ShepherdId(pid) && t.State == ThreadBlockedDelegate {
		t.Context.SetReturnValue(uint64(returnVal))
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}
