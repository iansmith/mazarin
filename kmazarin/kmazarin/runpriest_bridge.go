package main

// runpriest_bridge.go — Bridge between SVC handler and RunPriest worker goroutine.
//
// Same pattern as loadmaz_bridge.go: blocks the calling thread and dispatches
// heavy priest creation work from KernelIdleLoop's goroutine (growable stack).

import (
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// runPriestPending is set by BlockForRunPriest and checked by KernelIdleLoop.
var runPriestPending uint32

// runPriestDispatching is set while thread 0 is inside DispatchRunPriestWork.
// The timer ISR checks this to avoid preempting thread 0 mid-dispatch.
var runPriestDispatching uint32

func initRunPriestWorker() {
	ksyscall.RunPriestReq.BlockedTID = -1
}

// DispatchRunPriestWork is called from KernelIdleLoop to perform RunPriest work.
func DispatchRunPriestWork() bool {
	dispatched := false
	for {
		atomicFired := atomic.SwapUint32(&runPriestPending, 0) == 1
		hasPending := ksyscall.RunPriestReq.BlockedTID >= 0

		if !atomicFired && !hasPending {
			if dispatched {
				atomic.StoreUint32(&runPriestDispatching, 0)
			}
			return dispatched
		}

		tid := ksyscall.RunPriestReq.BlockedTID
		if tid < 0 {
			if dispatched {
				atomic.StoreUint32(&runPriestDispatching, 0)
			}
			return dispatched
		}

		atomic.StoreUint32(&runPriestDispatching, 1)

		req := ksyscall.RunPriestReq
		ksyscall.RunPriestReq.BlockedTID = -1

		serial.RawUARTPuts("[RP:work]")
		result := ksyscall.DoRunPriestWork(&req)

		wakeLoadMazThread(req.BlockedTID, result)
		serial.RawUARTPuts("[RP:done]")
		dispatched = true
	}
}

// BlockForRunPriest blocks the calling thread for a RunPriest request.
// Returns the next thread's context pointer.
//
//go:nosplit
//go:noinline
func BlockForRunPriest() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	ksyscall.RunPriestReq.BlockedTID = int32(t.TID)

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
		ksyscall.RunPriestReq.BlockedTID = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedLoadMaz

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	atomic.StoreUint32(&runPriestPending, 1)

	return uintptr(unsafe.Pointer(&next.Context))
}
