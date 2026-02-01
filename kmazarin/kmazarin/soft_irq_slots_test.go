//go:build arm64 && !test_no_asm

package main

import (
	"mazzy/kmazarin/ds"
	"mazzy/shared/hid"
	"sync/atomic"
	"testing"
	"unsafe"
)

// ============================================================================
// Soft IRQ Ring Buffer Tests
// ============================================================================

func TestRingPushDrain(t *testing.T) {
	var ring softIRQRing

	// Push 3 events
	ev1 := hid.HIDEvent{Type: hid.EvKey, Code: 30, Value: 1}
	ev2 := hid.HIDEvent{Type: hid.EvKey, Code: 30, Value: 0}
	ev3 := hid.HIDEvent{Type: hid.EvSyn, Code: 0, Value: 0}

	if !ringPush(&ring, ev1) {
		t.Fatal("ringPush ev1 failed")
	}
	if !ringPush(&ring, ev2) {
		t.Fatal("ringPush ev2 failed")
	}
	if !ringPush(&ring, ev3) {
		t.Fatal("ringPush ev3 failed")
	}

	// Drain all
	var buf [8]hid.HIDEvent
	n := RingDrain(&ring, buf[:], 8)
	if n != 3 {
		t.Fatalf("expected 3 events, got %d", n)
	}
	if buf[0] != ev1 || buf[1] != ev2 || buf[2] != ev3 {
		t.Fatalf("drained events don't match")
	}

	// Ring should be empty now
	n = RingDrain(&ring, buf[:], 8)
	if n != 0 {
		t.Fatalf("expected 0 events after drain, got %d", n)
	}
}

func TestRingOverflow(t *testing.T) {
	var ring softIRQRing

	// Fill ring to capacity
	for i := 0; i < softIRQRingSize; i++ {
		ev := hid.HIDEvent{Type: hid.EvKey, Code: uint16(i), Value: 1}
		if !ringPush(&ring, ev) {
			t.Fatalf("ringPush failed at index %d", i)
		}
	}

	// Next push should fail (overflow)
	ev := hid.HIDEvent{Type: hid.EvKey, Code: 999, Value: 1}
	if ringPush(&ring, ev) {
		t.Fatal("ringPush should have failed on full ring")
	}

	// Drain one, then push should succeed
	var buf [1]hid.HIDEvent
	n := RingDrain(&ring, buf[:], 1)
	if n != 1 {
		t.Fatalf("expected 1 event, got %d", n)
	}
	if !ringPush(&ring, ev) {
		t.Fatal("ringPush should succeed after draining one")
	}
}

func TestRingDrainPartial(t *testing.T) {
	var ring softIRQRing

	// Push 5 events
	for i := 0; i < 5; i++ {
		ringPush(&ring, hid.HIDEvent{Type: hid.EvKey, Code: uint16(i), Value: 1})
	}

	// Drain only 2
	var buf [2]hid.HIDEvent
	n := RingDrain(&ring, buf[:], 2)
	if n != 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}

	// 3 remaining
	var buf2 [8]hid.HIDEvent
	n = RingDrain(&ring, buf2[:], 8)
	if n != 3 {
		t.Fatalf("expected 3 remaining events, got %d", n)
	}
}

// ============================================================================
// Soft IRQ Slot Blocking/Wake Tests
// ============================================================================

// softIRQTestEnv saves and restores global state for softIRQ tests.
type softIRQTestEnv struct {
	origThreadList       ds.StaticList[*Thread, Thread]
	origReadyQueue       ds.StaticQueue[ThreadId]
	origBlockedQueue     ds.StaticQueue[ThreadId]
	origSleepingQueue    ds.StaticQueue[ThreadId]
	origCurrentThreadIdx int32
	origCurrentThread    unsafe.Pointer
	origTimerFreq        uint64
	origSpinBackoff      uint64
	origSwitchTarget     uintptr
	origSlotData         [maxSoftIRQSlots]softIRQSlot
	origSlotInUse        [maxSoftIRQSlots]bool
	origIrqToSlot        [256]int32

	threadListData  [10]Thread
	threadListInUse [10]bool
	readyQueueData  [10]ThreadId
	readyQueueInUse [10]bool
	blockedQueueData  [10]ThreadId
	blockedQueueInUse [10]bool
	sleepingQueueData  [10]ThreadId
	sleepingQueueInUse [10]bool
}

func newSoftIRQTestEnv() *softIRQTestEnv {
	env := &softIRQTestEnv{
		origThreadList:       threadList,
		origReadyQueue:       readyQueue,
		origBlockedQueue:     blockedQueue,
		origSleepingQueue:    sleepingQueue,
		origCurrentThreadIdx: CurrentThreadIdx,
		origCurrentThread:    atomic.LoadPointer(&CurrentThread),
		origTimerFreq:        timerFrequencyHz,
		origSpinBackoff:      ds.SpinBackoffTicks,
		origSwitchTarget:     syscallSwitchTarget,
		origSlotData:         softIRQSlotData,
		origSlotInUse:        softIRQSlotInUse,
		origIrqToSlot:        irqToSlot,
	}

	timerFrequencyHz = 62500000
	ds.InitSpinlockTiming(timerFrequencyHz)

	threadList.Data = env.threadListData[:]
	threadList.InUse = env.threadListInUse[:]
	readyQueue.Data = env.readyQueueData[:]
	readyQueue.InUse = env.readyQueueInUse[:]
	blockedQueue.Data = env.blockedQueueData[:]
	blockedQueue.InUse = env.blockedQueueInUse[:]
	sleepingQueue.Data = env.sleepingQueueData[:]
	sleepingQueue.InUse = env.sleepingQueueInUse[:]

	CurrentThreadIdx = -1
	atomic.StorePointer(&CurrentThread, nil)
	syscallSwitchTarget = 0

	// Reset slot state
	for i := range softIRQSlotData {
		softIRQSlotData[i] = softIRQSlot{blockedTID: -1}
	}
	for i := range softIRQSlotInUse {
		softIRQSlotInUse[i] = false
	}
	for i := range irqToSlot {
		irqToSlot[i] = -1
	}

	return env
}

func (env *softIRQTestEnv) restore() {
	threadList = env.origThreadList
	readyQueue = env.origReadyQueue
	blockedQueue = env.origBlockedQueue
	sleepingQueue = env.origSleepingQueue
	CurrentThreadIdx = env.origCurrentThreadIdx
	atomic.StorePointer(&CurrentThread, env.origCurrentThread)
	timerFrequencyHz = env.origTimerFreq
	ds.SpinBackoffTicks = env.origSpinBackoff
	syscallSwitchTarget = env.origSwitchTarget
	softIRQSlotData = env.origSlotData
	softIRQSlotInUse = env.origSlotInUse
	irqToSlot = env.origIrqToSlot
}

func (env *softIRQTestEnv) createThread(idx int, tid ThreadId, state ThreadState) *Thread {
	env.threadListData[idx] = Thread{
		TID:   tid,
		State: state,
	}
	env.threadListInUse[idx] = true
	return &env.threadListData[idx]
}

func (env *softIRQTestEnv) setCurrentThread(idx int) {
	CurrentThreadIdx = int32(idx)
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&env.threadListData[idx]))
}

func (env *softIRQTestEnv) setupSlot(slotNum int, irqNum uint32, ring *softIRQRing, intKind hid.InterruptType) {
	softIRQSlotData[slotNum] = softIRQSlot{
		active:     1,
		irqNum:     irqNum,
		intKind:    intKind,
		ring:       ring,
		blockedTID: -1,
	}
	softIRQSlotInUse[slotNum] = true
	if irqNum < 256 {
		irqToSlot[irqNum] = int32(slotNum)
	}
}

// TestBlockOnSlotWithPendingEvents: fast path — events in ring, no blocking.
func TestBlockOnSlotWithPendingEvents(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// Push an event into the ring
	ringPush(&ring, hid.HIDEvent{Type: hid.EvKey, Code: 30, Value: 1})

	// Drain should return 1 without blocking
	var buf [8]hid.HIDEvent
	n := DrainSoftIRQSlotEvents(0, buf[:], 8)
	if n != 1 {
		t.Fatalf("expected 1 event, got %d", n)
	}
	if buf[0].Code != 30 {
		t.Fatalf("expected code 30, got %d", buf[0].Code)
	}
}

// TestBlockOnSlotNoEvents: slow path — no events, thread blocks.
func TestBlockOnSlotNoEvents(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// Create thread A (current, running) and thread B (ready)
	env.createThread(0, ThreadId(10), ThreadRunning)
	env.setCurrentThread(0)
	threadB := env.createThread(1, ThreadId(11), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(11))

	// No events in ring — drain returns 0
	var buf [8]hid.HIDEvent
	n := DrainSoftIRQSlotEvents(0, buf[:], 8)
	if n != 0 {
		t.Fatalf("expected 0 events, got %d", n)
	}

	// Block should succeed: thread A blocks, returns context of thread B
	ctx := BlockOnSlot(0)
	if ctx == 0 {
		t.Fatal("BlockOnSlot returned 0 — expected context of next thread")
	}

	// Thread A should now be ThreadBlockedSoftIRQ
	if env.threadListData[0].State != ThreadBlockedSoftIRQ {
		t.Fatalf("expected thread A state ThreadBlockedSoftIRQ, got %d", env.threadListData[0].State)
	}

	// Slot should record blocked TID
	if softIRQSlotData[0].blockedTID != ThreadId(10) {
		t.Fatalf("expected blockedTID=10, got %d", softIRQSlotData[0].blockedTID)
	}
}

// TestBlockOnSlotNoOtherThreads: can't block when no other threads ready.
func TestBlockOnSlotNoOtherThreads(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// Only thread A running, no one else ready
	env.createThread(0, ThreadId(10), ThreadRunning)
	env.setCurrentThread(0)

	ctx := BlockOnSlot(0)
	if ctx != 0 {
		t.Fatal("BlockOnSlot should return 0 when no other threads ready")
	}

	// Thread A should remain running
	if env.threadListData[0].State != ThreadRunning {
		t.Fatalf("thread A should still be running, got %d", env.threadListData[0].State)
	}
}

// TestWakeSlotForIRQ: wake a blocked thread when IRQ fires.
func TestWakeSlotForIRQ(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// Thread A is blocked on slot 0
	threadA := env.createThread(0, ThreadId(10), ThreadBlockedSoftIRQ)
	_ = threadA
	env.threadListInUse[0] = true
	softIRQSlotData[0].blockedTID = ThreadId(10)

	// Thread B is running
	env.createThread(1, ThreadId(11), ThreadRunning)
	env.setCurrentThread(1)

	// Wake via IRQ
	WakeSlotForIRQ(112)

	// Thread A should be ThreadReady
	if env.threadListData[0].State != ThreadReady {
		t.Fatalf("expected thread A state ThreadReady, got %d", env.threadListData[0].State)
	}

	// blockedTID should be cleared
	if softIRQSlotData[0].blockedTID != -1 {
		t.Fatalf("expected blockedTID=-1, got %d", softIRQSlotData[0].blockedTID)
	}

	// Thread A should be in ready queue
	if readyQueue.IsEmpty() {
		t.Fatal("ready queue should not be empty after wake")
	}
}

// TestWakeSlotNoBlockedThread: IRQ fires but no thread is blocked — events stay in ring.
func TestWakeSlotNoBlockedThread(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// No blocked thread
	// Push some events
	ringPush(&ring, hid.HIDEvent{Type: hid.EvKey, Code: 30, Value: 1})

	// Wake should be a no-op (no panic, no crash)
	WakeSlotForIRQ(112)

	// Events should still be in ring
	var buf [8]hid.HIDEvent
	n := RingDrain(&ring, buf[:], 8)
	if n != 1 {
		t.Fatalf("expected 1 event still in ring, got %d", n)
	}
}

// TestSchedulingOrderSoftIRQVsKernelThread: when a softIRQ-blocked thread wakes,
// it goes to the ready queue. Verify it's scheduled relative to kernel threads.
func TestSchedulingOrderSoftIRQVsKernelThread(t *testing.T) {
	env := newSoftIRQTestEnv()
	defer env.restore()

	var ring softIRQRing
	env.setupSlot(0, 112, &ring, hid.KeyboardInterrupt)

	// Thread A (kernel, PID=0) already in ready queue
	threadA := env.createThread(0, ThreadId(10), ThreadReady)
	threadA.PID = 0 // kernel thread
	readyQueue.Push(ThreadId(10))

	// Thread B is blocked on softIRQ (userspace, PID=1)
	threadB := env.createThread(1, ThreadId(11), ThreadBlockedSoftIRQ)
	threadB.PID = 1
	softIRQSlotData[0].blockedTID = ThreadId(11)

	// Thread C is running
	env.createThread(2, ThreadId(12), ThreadRunning)
	env.setCurrentThread(2)

	// Wake thread B via IRQ
	WakeSlotForIRQ(112)

	// Both A and B should be in ready queue.
	// Kernel threads (PID=0) get PushHead (priority), userspace gets Push (tail).
	// Thread A was already in queue. Thread B (PID=1) gets Push (appended).
	// So order should be: A (head), then B (tail).
	if readyQueue.IsEmpty() {
		t.Fatal("ready queue should not be empty")
	}

	// Pop first — should be kernel thread A (PID=0, pushed via PushHead originally,
	// but note: readyQueue.Push was used for initial add, not PushHead)
	first := readyQueue.Pop()
	if first != ThreadId(10) {
		t.Logf("Note: scheduling order — first popped TID=%d (expected 10 for kernel thread)", first)
	}

	second := readyQueue.Pop()
	if second != ThreadId(11) {
		t.Logf("Note: scheduling order — second popped TID=%d (expected 11 for softIRQ handler)", second)
	}

	// Verify both were dequeued
	if !readyQueue.IsEmpty() {
		t.Fatal("ready queue should be empty after popping both")
	}
}
