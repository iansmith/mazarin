package main

// runshepherd_bridge.go — Bridge between SVC handler and RunShepherd worker goroutine.
//
// Same pattern as loadmaz_bridge.go: blocks the calling thread and dispatches
// heavy shepherd creation work from KernelIdleLoop's goroutine (growable stack).

import (
	"mazzy/kmazarin/ksyscall"
	"sync/atomic"
	"unsafe"
)

// runShepherdPending is set by BlockForRunShepherd and checked by KernelIdleLoop.
var runShepherdPending uint32

// runShepherdDispatching is set while thread 0 is inside DispatchRunShepherdWork.
// The timer ISR checks this to avoid preempting thread 0 mid-dispatch.
var runShepherdDispatching uint32

func initRunShepherdWorker() {
	ksyscall.RunShepherdReq.BlockedTID = -1
}

// DispatchRunShepherdWork is called from KernelIdleLoop to perform RunShepherd work.
func DispatchRunShepherdWork() bool {
	dispatched := false
	for {
		atomicFired := atomic.SwapUint32(&runShepherdPending, 0) == 1
		hasPending := ksyscall.RunShepherdReq.BlockedTID >= 0

		if !atomicFired && !hasPending {
			if dispatched {
				atomic.StoreUint32(&runShepherdDispatching, 0)
			}
			return dispatched
		}

		tid := ksyscall.RunShepherdReq.BlockedTID
		if tid < 0 {
			if dispatched {
				atomic.StoreUint32(&runShepherdDispatching, 0)
			}
			return dispatched
		}

		atomic.StoreUint32(&runShepherdDispatching, 1)

		req := ksyscall.RunShepherdReq
		ksyscall.RunShepherdReq.BlockedTID = -1
		atomic.StoreInt32(&ksyscall.RunShepherdBusy, 0)

		result := ksyscall.DoRunShepherdWork(&req)
		wakeLoadMazThread(req.BlockedTID, result)
		dispatched = true
	}
}

// BlockForRunShepherd blocks the calling thread for a RunShepherd request.
// Returns the next thread's context pointer.
//
//go:nosplit
//go:noinline
func BlockForRunShepherd() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	ksyscall.RunShepherdReq.BlockedTID = int32(t.TID)

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
		ksyscall.RunShepherdReq.BlockedTID = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedLoadMaz

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	atomic.StoreUint32(&runShepherdPending, 1)

	return uintptr(unsafe.Pointer(&next.Context))
}
