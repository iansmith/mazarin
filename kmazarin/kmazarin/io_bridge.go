package main

// io_bridge.go — Bridge between ksyscall block I/O and the scheduler.
//
// Follows the same pattern as ipc_bridge.go: ksyscall calls these functions
// via go:linkname to block/wake threads for interrupt-driven block I/O.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// BlockForBlockIO blocks the current thread waiting for block I/O completion.
// ioComplete is re-checked under the scheduler lock to prevent the missed-wakeup
// race (IRQ fires between submit and block).
// Returns the context pointer of the next thread to switch to, or 0 if I/O
// already completed. Thread 0 is always on the ready queue when a priest is
// running, so findReady should always succeed.
//
//go:nosplit
//go:noinline
func BlockForBlockIO(ioComplete *uint32) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Re-check: if the IRQ already fired between submit and here, don't block.
	if atomic.LoadUint32(ioComplete) != 0 {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Find next ready thread (prefer userspace if caller is userspace)
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
		// Thread 0 is always on the ready queue when a priest is running.
		// If we get here, the ready queue is broken.
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		serial.RawUARTPuts("[BlockForBlockIO] BUG: no ready thread (thread 0 missing from queue)\r\n")
		return 0
	}

	t.State = ThreadBlockedIO

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeBlockIOThread wakes a thread blocked in ThreadBlockedIO.
// Called from IRQ top-half (NonTimerIRQTopHalf) — must be nosplit.
//
//go:nosplit
//go:noinline
func WakeBlockIOThread(tid int32) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedIO {
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}
