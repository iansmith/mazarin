// kernel_worker.go — Generic bridge from SVC handler to worker goroutine.
//
// The kernel's SVC handlers run on the exception stack (SP_EL1) where Go's
// stack cannot grow. Heavy work (ELF loading, page table setup, etc.) needs
// a growable goroutine stack. This file provides a generic three-layer bridge:
//
//   1. SVC handler calls kw.Submit (stores request, blocks thread, sets atomic flag)
//   2. Thread 0's KernelIdleLoop calls kw.Relay (converts flag to channel send)
//   3. Worker goroutine receives, calls SVCWorker.Do, wakes blocked thread
//
// Channel operations cannot be used from exception/ISR context (not nosplit-safe,
// gopark corruption, write barrier hazards). Thread 0's idle loop is the necessary
// bridge between the exception world and Go's goroutine world.

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// SVCWorker is the interface each subsystem implements. The type parameter R
// is the per-subsystem request struct (e.g. LoadMazWorkRequest).
type SVCWorker[R any] interface {
	Do(req *R) int64
}

// KernelSVCWorker bridges nosplit SVC context to a worker goroutine.
type KernelSVCWorker[R any] struct {
	name    string
	pending uint32        // atomic: SVC handler → thread 0 relay
	busy    int32         // CAS: one request in flight
	req     R             // written by Submit, read by worker goroutine
	tid     int32         // blocked thread's TID (-1 = none)
	ch      chan struct{} // thread 0 relay → worker goroutine
	worker  SVCWorker[R]
}

// NewKernelSVCWorker creates a worker and starts its goroutine.
func NewKernelSVCWorker[R any](name string, w SVCWorker[R]) *KernelSVCWorker[R] {
	kw := &KernelSVCWorker[R]{
		name:   name,
		tid:    -1,
		ch:     make(chan struct{}, 1),
		worker: w,
	}
	go kw.run()
	return kw
}

// Submit is called from the SVC handler. It stores the request, blocks
// the calling thread, and sets the pending flag for thread 0 to relay.
// Returns the next thread's context pointer for SetSyscallSwitchTarget,
// or 0 on failure (busy or no ready thread).
func (kw *KernelSVCWorker[R]) Submit(req R) uintptr {
	if !atomic.CompareAndSwapInt32(&kw.busy, 0, 1) {
		return 0 // busy
	}
	kw.req = req
	atomic.StoreUint32(&kw.pending, 1)
	ctxPtr := blockAndSwitch(&kw.tid)
	if ctxPtr == 0 {
		// No thread to switch to — undo.
		atomic.StoreInt32(&kw.busy, 0)
		kw.tid = -1
	}
	return ctxPtr
}

// Relay is called from thread 0's KernelIdleLoop. If the pending flag
// is set, it sends on the channel to wake the worker goroutine.
func (kw *KernelSVCWorker[R]) Relay() {
	if atomic.SwapUint32(&kw.pending, 0) == 1 {
		select {
		case kw.ch <- struct{}{}:
		default:
		}
	}
}

// Pending returns true if work is waiting to be relayed. Used by
// hasPendingKernelWork for thread 0 boost decisions.
//
//go:nosplit
func (kw *KernelSVCWorker[R]) Pending() bool {
	return atomic.LoadUint32(&kw.pending) != 0
}

// run is the worker goroutine. It blocks on the channel, executes
// the subsystem's Do method, and wakes the blocked thread with the result.
func (kw *KernelSVCWorker[R]) run() {
	for range kw.ch {
		req := kw.req
		tid := kw.tid
		kw.tid = -1

		result := kw.worker.Do(&req)

		wakeBlockedThread(tid, result)
		atomic.StoreInt32(&kw.busy, 0)
	}
}

// blockAndSwitch blocks the calling thread and returns any ready thread's
// context pointer. This is the only nosplit piece — it touches the scheduler
// lock and thread state.
//
//go:nosplit
//go:noinline
func blockAndSwitch(tidOut *int32) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	*tidOut = int32(t.TID)

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		// Undo — nobody to switch to.
		*tidOut = -1
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedKernelWork

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
	return uintptr(unsafe.Pointer(&next.Context))
}

// wakeBlockedThread wakes a thread in ThreadBlockedKernelWork state,
// injecting the syscall return value.
func wakeBlockedThread(tid int32, result int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && t.State == ThreadBlockedKernelWork {
		t.Context.SetReturnValue(uint64(result))
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()
	} else {
		serial.RawUARTPuts("[KW:wake FAIL tid=")
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

// --- Worker implementations (one per subsystem) ---

type loadMazWorkerImpl struct{}

func (loadMazWorkerImpl) Do(req *ksyscall.LoadMazWorkRequest) int64 {
	return ksyscall.DoLoadMazWork(req)
}

type runMazWorkerImpl struct{}

func (runMazWorkerImpl) Do(req *ksyscall.RunMazWorkRequest) int64 {
	return ksyscall.DoRunMazWork(req)
}

type runShepherdWorkerImpl struct{}

func (runShepherdWorkerImpl) Do(req *ksyscall.RunShepherdWorkRequest) int64 {
	return ksyscall.DoRunShepherdWork(req)
}

type epollWorkerImpl struct{}

func (epollWorkerImpl) Do(req *ksyscall.EpollWorkRequest) int64 {
	return ksyscall.DoEpollCtlWork(req)
}

// --- Global instances ---

var (
	loadMazKW     *KernelSVCWorker[ksyscall.LoadMazWorkRequest]
	runMazKW      *KernelSVCWorker[ksyscall.RunMazWorkRequest]
	runShepherdKW *KernelSVCWorker[ksyscall.RunShepherdWorkRequest]
	epollKW       *KernelSVCWorker[ksyscall.EpollWorkRequest]
)

// initKernelWorkers creates all kernel SVC workers and starts their goroutines.
// Called from simpleMain before entering KernelIdleLoop.
func initKernelWorkers() {
	loadMazKW = NewKernelSVCWorker("LoadMaz", loadMazWorkerImpl{})
	runMazKW = NewKernelSVCWorker("RunMaz", runMazWorkerImpl{})
	runShepherdKW = NewKernelSVCWorker("RunShepherd", runShepherdWorkerImpl{})
	epollKW = NewKernelSVCWorker("Epoll", epollWorkerImpl{})
}

// --- Linkname bridge wrappers (ksyscall → main) ---

// SubmitLoadMaz is the linkname target for ksyscall.submitLoadMaz.
func SubmitLoadMaz(req ksyscall.LoadMazWorkRequest) uintptr {
	return loadMazKW.Submit(req)
}

// SubmitRunMaz is the linkname target for ksyscall.submitRunMaz.
func SubmitRunMaz(req ksyscall.RunMazWorkRequest) uintptr {
	return runMazKW.Submit(req)
}

// SubmitRunShepherd is the linkname target for ksyscall.submitRunShepherd.
func SubmitRunShepherd(req ksyscall.RunShepherdWorkRequest) uintptr {
	return runShepherdKW.Submit(req)
}

// SubmitEpoll is the linkname target for ksyscall.submitEpoll.
func SubmitEpoll(req ksyscall.EpollWorkRequest) uintptr {
	return epollKW.Submit(req)
}

// hasPendingKernelWork returns true if any SVC worker has pending work
// waiting for thread 0 to relay. Used by the timer ISR to boost thread 0.
//
//go:nosplit
func hasPendingKernelWork() bool {
	if loadMazKW == nil {
		return false // called before initKernelWorkers
	}
	return loadMazKW.Pending() || runMazKW.Pending() || runShepherdKW.Pending()
}
