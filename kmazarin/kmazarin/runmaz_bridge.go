package main

// runmaz_bridge.go — Bridge between SVC handler and RunMaz worker goroutine.
//
// Same pattern as loadmaz_bridge.go: blocks the calling thread and dispatches
// heavy ELF work from KernelIdleLoop's goroutine (growable stack).

import (
	"mazzy/kmazarin/ksyscall"
	"sync/atomic"
	"unsafe"
)

// runMazPending is set by BlockForRunMaz and checked by KernelIdleLoop.
var runMazPending uint32

// runMazDispatching is set while thread 0 is inside DispatchRunMazWork.
// The timer ISR checks this to avoid preempting thread 0 mid-dispatch.
var runMazDispatching uint32

func initRunMazWorker() {
	ksyscall.RunMazReq.BlockedTID = -1
}

// DispatchRunMazWork is called from KernelIdleLoop to perform RunMaz work.
func DispatchRunMazWork() bool {
	dispatched := false
	for {
		atomicFired := atomic.SwapUint32(&runMazPending, 0) == 1
		hasPending := ksyscall.RunMazReq.BlockedTID >= 0

		if !atomicFired && !hasPending {
			if dispatched {
				atomic.StoreUint32(&runMazDispatching, 0)
			}
			return dispatched
		}

		tid := ksyscall.RunMazReq.BlockedTID
		if tid < 0 {
			if dispatched {
				atomic.StoreUint32(&runMazDispatching, 0)
			}
			return dispatched
		}

		atomic.StoreUint32(&runMazDispatching, 1)

		req := ksyscall.RunMazReq
		ksyscall.RunMazReq.BlockedTID = -1
		atomic.StoreInt32(&ksyscall.RunMazBusy, 0)

		result := ksyscall.DoRunMazWork(&req)
		wakeLoadMazThread(req.BlockedTID, result)
		dispatched = true
	}
}

// BlockForRunMaz blocks the calling thread for a RunMaz request.
// Returns the next thread's context pointer.
//
//go:nosplit
//go:noinline
func BlockForRunMaz() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	ksyscall.RunMazReq.BlockedTID = int32(t.TID)

	// Switch to thread 0 (kernel idle loop)
	var next *Thread
	thread0 := threadLookupByTID(0)
	if thread0 != nil && thread0.State == ThreadReady {
		pluckFromAllQueues(thread0.TID)
		next = thread0
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		ksyscall.RunMazReq.BlockedTID = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedLoadMaz

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	atomic.StoreUint32(&runMazPending, 1)

	return uintptr(unsafe.Pointer(&next.Context))
}
