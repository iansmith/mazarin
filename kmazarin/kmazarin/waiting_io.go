package main

// waiting_io.go — WaitingIO queue for threads blocked on TX ring full.
//
// When write(fd=1/2) finds the PL011 TX ring buffer completely full, the
// calling thread is blocked on ThreadBlockedWaitingIO and enqueued here.
// The TX interrupt top-half (uart_tophalf_arm64.go) drains from the head
// thread's kernel buffer into the TX ring. When the request is fully
// consumed, the top-half dequeues and wakes the thread exactly once.
//
// Concurrency model:
//   - Enqueue: called with schedulerLock held (normal syscall context)
//   - Peek/Dequeue: called with TxTryLock held (nosplit TX interrupt top-half)
//   - waitingIOCount: atomic int32, coordination point between the two

import (
	"sync/atomic"
	"unsafe"
)

const waitingIOQueueSize = 8

var (
	waitingIOQueue [waitingIOQueueSize]uintptr // *Thread stored as uintptr (no GC write barrier)
	waitingIOHead  int32
	waitingIOTail  int32
	waitingIOCount int32 // atomic: read from top-half to skip when empty
)

// waitingIOEnqueue adds a thread to the WaitingIO queue.
// Called from BlockForWaitingIO with schedulerLock held.
func waitingIOEnqueue(t *Thread) {
	if atomic.LoadInt32(&waitingIOCount) >= waitingIOQueueSize {
		return // full — shouldn't happen in practice
	}
	waitingIOQueue[waitingIOTail] = uintptr(unsafe.Pointer(t))
	waitingIOTail = (waitingIOTail + 1) % waitingIOQueueSize
	atomic.AddInt32(&waitingIOCount, 1)
}

// waitingIOPeekHead returns the front thread without removing it.
// Returns nil if the queue is empty.
// Called from the nosplit TX interrupt top-half with TxTryLock held.
//
//go:nosplit
func waitingIOPeekHead() *Thread {
	if atomic.LoadInt32(&waitingIOCount) <= 0 {
		return nil
	}
	return (*Thread)(unsafe.Pointer(waitingIOQueue[waitingIOHead]))
}

// waitingIODequeueHead removes the front entry from the queue.
// Called from the nosplit TX interrupt top-half with TxTryLock held,
// after the head thread's WaitingIO request has been fully consumed.
//
//go:nosplit
func waitingIODequeueHead() {
	waitingIOQueue[waitingIOHead] = 0
	waitingIOHead = (waitingIOHead + 1) % waitingIOQueueSize
	atomic.AddInt32(&waitingIOCount, -1)
}
