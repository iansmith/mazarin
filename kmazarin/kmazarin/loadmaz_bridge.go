package main

// loadmaz_bridge.go — Bridge between SVC handler (g0/SP_EL1) and heavy .maz loading.
//
// SyscallLoadMaz runs on g0 (the exception stack) where the Go stack cannot
// grow. The heavy work (FAT32 I/O, ELF segment loading, PIE relocations) needs
// a growable stack. This bridge blocks the calling thread and dispatches the
// work directly from KernelIdleLoop's goroutine (which has a normal growable stack).
//
// Protocol:
//   1. SyscallLoadMaz (ksyscall, on g0): stores request, calls BlockForLoadMaz
//   2. BlockForLoadMaz (here): blocks thread, sets loadMazPending atomic
//   3. KernelIdleLoop (threads.go): calls DispatchLoadMazWork (here)
//   4. DispatchLoadMazWork: calls ksyscall.DoLoadMazWork, wakes blocked thread

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// loadMazPending is set by BlockForLoadMaz (from g0 context) and checked
// by KernelIdleLoop to dispatch .maz loading work.
var loadMazPending uint32

// loadMazDispatching is set while thread 0 is inside DispatchLoadMazWork.
// The timer ISR checks this to avoid preempting thread 0 mid-dispatch,
// which would starve it (the userspace-preferring scheduler never picks
// thread 0 from the ready queue).
var loadMazDispatching uint32

// initLoadMazWorker initializes the .maz loading bridge.
// Called from simpleMain before entering KernelIdleLoop.
func initLoadMazWorker() {
	// Initialize BlockedTID to -1 (no pending request).
	ksyscall.LoadMazReq.BlockedTID = -1
}

// DispatchLoadMazWork is called from KernelIdleLoop (on the main goroutine
// with a growable stack) to perform the heavy .maz loading work.
// Loops internally to process all pending requests before returning,
// and sets loadMazDispatching to prevent timer preemption of thread 0
// while we're dispatching (otherwise the userspace-preferring scheduler
// starves thread 0 and subsequent requests never get processed).
// Returns true if any work was dispatched.
func DispatchLoadMazWork() bool {
	dispatched := false
	for {
		// Check both the atomic flag AND the BlockedTID as a belt-and-suspenders
		// approach. The atomic flag is the primary signal; BlockedTID >= 0 is backup.
		atomicFired := atomic.SwapUint32(&loadMazPending, 0) == 1
		hasPending := ksyscall.LoadMazReq.BlockedTID >= 0

		if !atomicFired && !hasPending {
			if dispatched {
				atomic.StoreUint32(&loadMazDispatching, 0)
			}
			return dispatched
		}

		tid := ksyscall.LoadMazReq.BlockedTID
		if tid < 0 {
			// Atomic flag fired but no valid TID — spurious, ignore.
			if dispatched {
				atomic.StoreUint32(&loadMazDispatching, 0)
			}
			return dispatched
		}

		// Set anti-preemption flag so the timer ISR doesn't preempt us
		// between request completions.
		atomic.StoreUint32(&loadMazDispatching, 1)

		if atomicFired {
			serial.RawUARTPuts("[LM:atomic]")
		} else {
			serial.RawUARTPuts("[LM:tid]")
		}

		// Snapshot the request and clear the TID to prevent re-dispatch.
		req := ksyscall.LoadMazReq
		ksyscall.LoadMazReq.BlockedTID = -1
		atomic.StoreInt32(&ksyscall.LoadMazBusy, 0)

		serial.RawUARTPuts("[LM:work]")
		result := ksyscall.DoLoadMazWork(&req)

		// Wake the blocked thread with the result.
		wakeLoadMazThread(req.BlockedTID, result)
		serial.RawUARTPuts("[LM:done]")
		dispatched = true
		// Loop back to check for more pending requests.
	}
}

// BlockForLoadMaz blocks the calling kernel thread for a .maz load request.
// Sets the loadMazPending flag so KernelIdleLoop dispatches to the worker.
// Returns the next thread's context pointer (for SetSyscallSwitchTarget).
//
// Called from ksyscall.SyscallLoadMaz via go:linkname.
//
//go:nosplit
//go:noinline
func BlockForLoadMaz() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Store the blocked thread's TID in the request so the worker can wake it.
	ksyscall.LoadMazReq.BlockedTID = int32(t.TID)

	// Switch to thread 0 (kernel idle loop) specifically. Thread 0 runs
	// KernelIdleLoop on the main goroutine with a growable stack, which is
	// where DispatchLoadMazWork executes. If we switch to another userspace
	// thread, thread 0 may be starved by the userspace-preferring scheduler
	// and never get CPU time to dispatch the work.
	var next *Thread
	thread0 := threadLookupByTID(0)
	if thread0 != nil && thread0.State == ThreadReady {
		pluckFromAllQueues(thread0.TID)
		next = thread0
	} else {
		// Thread 0 not ready (shouldn't happen in practice) — fall back to any thread.
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		// Can't block — no other thread to switch to.
		ksyscall.LoadMazReq.BlockedTID = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Commit: block current thread.
	t.State = ThreadBlockedLoadMaz

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	// Set the pending flag AFTER releasing the scheduler lock.
	// KernelIdleLoop will see this and call DispatchLoadMazWork.
	atomic.StoreUint32(&loadMazPending, 1)
	serial.RawUARTPuts("[LM:pend]")

	return uintptr(unsafe.Pointer(&next.Context))
}

// wakeLoadMazThread wakes a thread blocked in ThreadBlockedLoadMaz state,
// setting its return value so the shepherd sees success (0) or an error code.
func wakeLoadMazThread(tid int32, result int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedLoadMaz {
		serial.RawUARTPuts("[LM:wake TID=")
		serial.RawUARTHex64(uint64(tid))
		serial.RawUARTPuts("]\r\n")
		t.Context.SetReturnValue(uint64(result))
		// Give the woken thread a fresh preemption quantum so it
		// isn't immediately preempted due to stale elapsed time.
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	} else {
		serial.RawUARTPuts("[LM:wake FAIL tid=")
		serial.RawUARTHex64(uint64(tid))
		if t == nil {
			serial.RawUARTPuts(" t=nil")
		} else {
			serial.RawUARTPuts(" state=")
			serial.RawUARTHex64(uint64(t.State))
		}
		serial.RawUARTPuts("]\r\n")
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}
