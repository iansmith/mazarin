// kernel_worker.go — Generic bridge from SVC handler to worker goroutine.
//
// The kernel's SVC handlers run on the exception stack (SP_EL1) where Go's
// stack cannot grow. Heavy work (ELF loading, page table setup, etc.) needs
// a growable goroutine stack. This file provides a generic two-layer bridge:
//
//   1. SVC handler calls kw.Submit (stores request, blocks thread, sets atomic flag)
//   2. Thread 0's KernelIdleLoop calls kw.Relay → run (executes work, wakes thread)
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

// KernelSVCWorker bridges nosplit SVC context to thread 0's growable stack.
//
// CURRENT STATE: Thread 0 calls run() directly from Relay(). This means
// run() executes inline on thread 0's stack, which can cause several missed
// timer ticks for heavy operations (ELF loading, shepherd launch). We accept
// this tradeoff for now.
//
// FUTURE DESIGN: run() should be called from a worker goroutine that reads
// from a channel, not directly by thread 0. The Relay() method would send
// on the channel, and the worker goroutine would receive and call run().
// This requires solving the goroutine scheduling problem: thread 0's idle
// loop never calls runtime.Gosched() (doing so causes context-switch
// corruption when the Go scheduler picks sysmon), so worker goroutines
// currently never get scheduled. The solution is to have the timer top-half
// detect hasPendingKernelWork() and trigger async preemption of the idle
// loop goroutine, allowing the Go scheduler to pick the worker goroutine.
// Once that mechanism exists, both cooperative preemption and async
// preemption should be enabled during the run() body so the kernel
// remains responsive to timer interrupts and GC requests.
type KernelSVCWorker[R any] struct {
	name        string
	pending     uint32 // atomic: SVC handler → thread 0 relay
	busy        int32  // CAS: one request in flight
	dispatching uint32 // atomic: set while run() is executing (anti-preempt guard)
	req         R      // written by Submit, read by Relay
	tid         int32  // blocked thread's TID (-1 = none)
	worker      SVCWorker[R]
}

// NewKernelSVCWorker creates a worker. Work is dispatched inline by Relay()
// for now; see KernelSVCWorker doc comment for future channel-based design.
func NewKernelSVCWorker[R any](name string, w SVCWorker[R]) *KernelSVCWorker[R] {
	return &KernelSVCWorker[R]{
		name:   name,
		tid:    -1,
		worker: w,
	}
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
// is set, extracts the request and calls run() to execute the work
// directly on thread 0's growable stack.
//
// FUTURE: Instead of calling run() directly, this should send on a
// channel to wake a worker goroutine that calls run(). See the
// KernelSVCWorker doc comment for the full design.
func (kw *KernelSVCWorker[R]) Relay() {
	if atomic.SwapUint32(&kw.pending, 0) != 1 {
		return
	}

	req := kw.req
	tid := kw.tid
	kw.tid = -1

	kw.run(&req, tid)
}

// Pending returns true if work is waiting to be relayed. Used by
// hasPendingKernelWork for thread 0 boost decisions.
//
//go:nosplit
func (kw *KernelSVCWorker[R]) Pending() bool {
	return atomic.LoadUint32(&kw.pending) != 0
}

// Dispatching returns true if this worker is currently executing run().
// Used by the timer ISR to avoid preempting thread 0 mid-dispatch.
//
//go:nosplit
func (kw *KernelSVCWorker[R]) Dispatching() bool {
	return atomic.LoadUint32(&kw.dispatching) != 0
}

// setup is called at the start of run() to mark thread 0 as dispatching.
// The dispatching flag prevents the timer ISR from preempting thread 0
// mid-work (the userspace-preferring scheduler would starve thread 0,
// preventing subsequent requests from completing).
func (kw *KernelSVCWorker[R]) setup() {
	atomic.StoreUint32(&kw.dispatching, 1)
}

// tearDown is called (via defer) at the end of run() to clear the
// dispatching flag and release the busy lock.
func (kw *KernelSVCWorker[R]) tearDown() {
	atomic.StoreInt32(&kw.busy, 0)
	atomic.StoreUint32(&kw.dispatching, 0)
}

// run executes the subsystem's Do method and wakes the blocked thread
// with the result. Called directly by Relay() on thread 0 for now.
//
// FUTURE: This same method will be called by a worker goroutine that
// reads from a channel. The goroutine path would call run() with the
// same arguments after receiving from the channel. Both the thread 0
// path (Relay → run) and the goroutine path (channel receive → run)
// share this method. When the goroutine path is active, both cooperative
// and async preemption should be enabled during the Do() call so the
// kernel remains responsive.
func (kw *KernelSVCWorker[R]) run(req *R, tid int32) {
	kw.setup()
	defer kw.tearDown()

	result := kw.worker.Do(req)

	wakeBlockedThread(tid, result)
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

type uringConnectWorkerImpl struct{}

func (uringConnectWorkerImpl) Do(req *UringConnectWorkRequest) int64 {
	return DoUringConnectWork(req)
}

// --- Global instances ---

var (
	loadMazKW      *KernelSVCWorker[ksyscall.LoadMazWorkRequest]
	runMazKW       *KernelSVCWorker[ksyscall.RunMazWorkRequest]
	runShepherdKW  *KernelSVCWorker[ksyscall.RunShepherdWorkRequest]
	epollKW        *KernelSVCWorker[ksyscall.EpollWorkRequest]
	uringConnectKW *KernelSVCWorker[UringConnectWorkRequest]
)

// initKernelWorkers creates all kernel SVC workers.
// Called from simpleMain before entering KernelIdleLoop.
func initKernelWorkers() {
	loadMazKW = NewKernelSVCWorker("LoadMaz", loadMazWorkerImpl{})
	runMazKW = NewKernelSVCWorker("RunMaz", runMazWorkerImpl{})
	runShepherdKW = NewKernelSVCWorker("RunShepherd", runShepherdWorkerImpl{})
	epollKW = NewKernelSVCWorker("Epoll", epollWorkerImpl{})
	uringConnectKW = NewKernelSVCWorker("UringConnect", uringConnectWorkerImpl{})
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

// SubmitUringConnect is the linkname target for ksyscall.submitUringConnect.
func SubmitUringConnect(req UringConnectWorkRequest) uintptr {
	return uringConnectKW.Submit(req)
}

// hasPendingKernelWork returns true if any SVC worker has pending work
// waiting for thread 0 to relay. Used by the timer ISR to boost thread 0.
//
//go:nosplit
func hasPendingKernelWork() bool {
	if loadMazKW == nil {
		return false // called before initKernelWorkers
	}
	return loadMazKW.Pending() || runMazKW.Pending() || runShepherdKW.Pending() || uringConnectKW.Pending()
}
