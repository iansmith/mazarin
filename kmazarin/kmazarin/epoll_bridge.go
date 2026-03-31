package main

// epoll_bridge.go — Bridge between SVC handler and epoll worker goroutine.
//
// Same pattern as runshepherd_bridge.go: blocks the calling thread and dispatches
// heavy epoll_ctl work from KernelIdleLoop's goroutine (growable stack).
// The goroutine path allows calling DemandMapUserPage when ReadUserUint64 fails
// because the stack page containing ev.Data is not yet demand-mapped.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// epollPending is set by BlockForEpoll and checked by KernelIdleLoop.
var epollPending uint32

// epollDispatching is set while thread 0 is inside DispatchEpollWork.
// The timer ISR checks this to avoid preempting thread 0 mid-dispatch.
var epollDispatching uint32

func initEpollWorker() {
	ksyscall.EpollReq.BlockedTID = -1
}

// DispatchEpollWork is called from KernelIdleLoop to perform epoll_ctl work.
func DispatchEpollWork() bool {
	dispatched := false
	for {
		atomicFired := atomic.SwapUint32(&epollPending, 0) == 1
		hasPending := ksyscall.EpollReq.BlockedTID >= 0

		if !atomicFired && !hasPending {
			if dispatched {
				atomic.StoreUint32(&epollDispatching, 0)
			}
			return dispatched
		}

		tid := ksyscall.EpollReq.BlockedTID
		if tid < 0 {
			if dispatched {
				atomic.StoreUint32(&epollDispatching, 0)
			}
			return dispatched
		}

		atomic.StoreUint32(&epollDispatching, 1)

		req := ksyscall.EpollReq
		ksyscall.EpollReq.BlockedTID = -1
		atomic.StoreInt32(&ksyscall.EpollBusy, 0)

		result := ksyscall.DoEpollCtlWork(&req)
		wakeEpollThread(req.BlockedTID, result)
		dispatched = true
	}
}

// BlockForEpoll blocks the calling thread for an epoll_ctl request.
// Returns the next thread's context pointer.
//
//go:nosplit
//go:noinline
func BlockForEpoll() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	ksyscall.EpollReq.BlockedTID = int32(t.TID)

	// Switch to thread 0 (kernel idle loop) for prompt dispatch.
	var next *Thread
	thread0 := threadLookupByTID(0)
	if thread0 != nil && thread0.State == ThreadReady {
		pluckFromAllQueues(thread0.TID)
		next = thread0
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		ksyscall.EpollReq.BlockedTID = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedEpoll

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	atomic.StoreUint32(&epollPending, 1)

	return uintptr(unsafe.Pointer(&next.Context))
}

// wakeEpollThread wakes a thread blocked in ThreadBlockedEpoll state
// and injects the syscall return value.
func wakeEpollThread(tid int32, result int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedEpoll {
		t.Context.SetReturnValue(uint64(result))
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	} else {
		serial.RawUARTPuts("[EPL:wake FAIL tid=")
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
