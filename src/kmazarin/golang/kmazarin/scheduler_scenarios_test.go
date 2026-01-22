//go:build arm64 && !test_no_asm

package main

import (
	"kmazarin/ds"
	"sync/atomic"
	"testing"
	"unsafe"
)

// ============================================================================
// Test Helpers
// ============================================================================

// schedulerTestEnv holds test environment state for cleanup
type schedulerTestEnv struct {
	origThreadList       ds.StaticOrderedList[Thread, ThreadId]
	origReadyQueue       ds.StaticFIFO[ThreadId]
	origBlockedQueue     ds.StaticFIFO[ThreadId]
	origSleepingQueue    ds.StaticFIFO[ThreadId]
	origCurrentThreadIdx int32
	origCurrentThread    unsafe.Pointer
	origTimerFreq        uint64
	origSpinBackoff      uint64
	origSwitchTarget     uintptr

	// Test data
	threadListData [10]Thread
	threadListInUse [10]bool
	readyQueueData [10]ThreadId
	readyQueueInUse [10]bool
	blockedQueueData [10]ThreadId
	blockedQueueInUse [10]bool
	sleepingQueueData [10]ThreadId
	sleepingQueueInUse [10]bool
}

func newSchedulerTestEnv() *schedulerTestEnv {
	env := &schedulerTestEnv{
		origThreadList:       threadList,
		origReadyQueue:       readyQueue,
		origBlockedQueue:     blockedQueue,
		origSleepingQueue:    sleepingQueue,
		origCurrentThreadIdx: CurrentThreadIdx,
		origCurrentThread:    atomic.LoadPointer(&CurrentThread),
		origTimerFreq:        timerFrequencyHz,
		origSpinBackoff:      ds.SpinBackoffTicks,
		origSwitchTarget:     syscallSwitchTarget,
	}

	// Initialize test structures
	timerFrequencyHz = 62500000 // 62.5MHz
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

	return env
}

func (env *schedulerTestEnv) restore() {
	threadList = env.origThreadList
	readyQueue = env.origReadyQueue
	blockedQueue = env.origBlockedQueue
	sleepingQueue = env.origSleepingQueue
	CurrentThreadIdx = env.origCurrentThreadIdx
	atomic.StorePointer(&CurrentThread, env.origCurrentThread)
	timerFrequencyHz = env.origTimerFreq
	ds.SpinBackoffTicks = env.origSpinBackoff
	syscallSwitchTarget = env.origSwitchTarget
}

func (env *schedulerTestEnv) createThread(idx int, tid ThreadId, state ThreadState) *Thread {
	env.threadListData[idx] = Thread{
		TID:   tid,
		State: state,
	}
	env.threadListInUse[idx] = true
	return &env.threadListData[idx]
}

func (env *schedulerTestEnv) setCurrentThread(idx int) {
	CurrentThreadIdx = int32(idx)
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&env.threadListData[idx]))
}

func newTestSchedulerFunc(t *testing.T, times []uint64) (*SchedulerFunc, *DAIFPair, *TestClock) {
	daifPair := &DAIFPair{}
	testClock := NewTestClock(times)

	sf := &SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck: func(checkpoint string) {
			t.Logf("StateCheck: %s", checkpoint)
		},
	}
	return sf, daifPair, testClock
}

// checkInvariants verifies universal invariants after every test
func checkInvariants(t *testing.T, daifPair *DAIFPair) {
	t.Helper()
	if !daifPair.Balanced() {
		t.Errorf("INVARIANT VIOLATION: DAIF unbalanced: %d", daifPair.daifCalls)
	}
	// Note: Can't easily check schedulerLock without exposing it
}

// ============================================================================
// Test 1: Single thread running, no preemption (timeslice not exhausted)
// ============================================================================

func TestSingleThreadNoPreemption(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Create thread A (running, timeslice NOT exhausted)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	threadA.StartTick = 1000
	env.setCurrentThread(0)

	// Time is only slightly advanced (not enough to trigger preemption)
	// 100ms threshold @ 62.5MHz = 6,250,000 ticks
	currentTime := uint64(1000 + 1000000) // Only 1M ticks elapsed (16ms)
	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{currentTime})

	t.Log("Scenario: Thread A running, timeslice NOT exhausted, no other threads")
	t.Logf("  Elapsed: %d ticks (threshold ~6.25M)", currentTime-threadA.StartTick)

	// Call preemption check
	result := checkThreadPreemptionInternal(sf, 0)

	// Verify: No switch should occur (only one thread, and it's running)
	// Note: checkThreadPreemptionInternal is called AFTER NeedsThreadPreempt is set,
	// so it will try to preempt regardless of time. But with no other ready threads,
	// it should return 0 and keep current thread running.

	if result != 0 {
		t.Errorf("Expected no switch (result=0), got 0x%x", result)
	}

	if CurrentThreadIdx != 0 {
		t.Errorf("CurrentThreadIdx should still be 0, got %d", CurrentThreadIdx)
	}

	if threadA.State != ThreadRunning {
		t.Errorf("Thread A should still be Running, got state=%d", threadA.State)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Single thread continues running when no other threads ready")
}

// ============================================================================
// Test 2: Single thread, timeslice exhausted, no other threads ready
// ============================================================================

func TestSingleThreadExhaustedNoOthers(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Create thread A (running, timeslice exhausted)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	threadA.StartTick = 1000
	env.setCurrentThread(0)

	// Time well past threshold
	currentTime := uint64(1000 + 10000000) // 10M ticks (160ms)
	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{currentTime, currentTime + 1})

	t.Log("Scenario: Thread A exhausted timeslice, but no other threads exist")
	t.Logf("  Elapsed: %d ticks (threshold ~6.25M)", currentTime-threadA.StartTick)

	result := checkThreadPreemptionInternal(sf, 0)

	// Should return 0 (no switch possible) and thread continues
	if result != 0 {
		t.Errorf("Expected no switch (result=0) since no other threads, got 0x%x", result)
	}

	if threadA.State != ThreadRunning {
		t.Errorf("Thread A should be Running (only option), got state=%d", threadA.State)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Single exhausted thread continues when no alternative")
}

// ============================================================================
// Test 3: Two threads, running thread exhausts timeslice -> switch
// Already covered by TestSimplePreemption and TestThreadPreemption
// ============================================================================

func TestPreemptionWithReadyThread(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running (exhausted)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	threadA.StartTick = 1000
	env.setCurrentThread(0)

	// Thread B ready
	threadB := env.createThread(1, ThreadId(200), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(200))

	currentTime := uint64(1000 + 10000000) // Exhausted
	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{currentTime, currentTime + 1})

	t.Log("Scenario: Thread A exhausted, Thread B ready -> switch to B")

	result := checkThreadPreemptionInternal(sf, 0)

	if result == 0 {
		t.Error("Expected context switch, got 0")
	}

	// B should now be running
	if env.threadListData[1].State != ThreadRunning {
		t.Errorf("Thread B should be Running, got %d", env.threadListData[1].State)
	}

	// A should be ready
	if env.threadListData[0].State != ThreadReady {
		t.Errorf("Thread A should be Ready, got %d", env.threadListData[0].State)
	}

	// Current should be B
	current := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if current.TID != ThreadId(200) {
		t.Errorf("CurrentThread should be B (TID=200), got TID=%d", current.TID)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Preemption switches to ready thread")
}

// ============================================================================
// Test 5: Thread exits -> switch to next ready
// ============================================================================

func TestThreadExitSwitchToReady(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running (will exit)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	_ = threadA
	env.setCurrentThread(0)

	// Thread B ready
	threadB := env.createThread(1, ThreadId(200), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(200))

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000, 1001})

	t.Log("Scenario: Thread A exits, Thread B ready -> switch to B")

	result := ThreadExit(sf)

	if result == 0 {
		t.Error("Expected switch to B, got 0")
	}

	// A should be exited
	if env.threadListData[0].State != ThreadExited {
		t.Errorf("Thread A should be Exited, got %d", env.threadListData[0].State)
	}

	// B should be running
	if env.threadListData[1].State != ThreadRunning {
		t.Errorf("Thread B should be Running, got %d", env.threadListData[1].State)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Exit switches to ready thread")
}

// ============================================================================
// Test 6: Thread exits, no other threads -> return 0
// ============================================================================

func TestThreadExitNoOthers(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running (will exit), no others
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	_ = threadA
	env.setCurrentThread(0)

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000})

	t.Log("Scenario: Thread A exits, no other threads -> return 0 (halt)")

	result := ThreadExit(sf)

	if result != 0 {
		t.Errorf("Expected 0 (no threads), got 0x%x", result)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Exit returns 0 when no other threads")
}

// ============================================================================
// Test 7: Thread blocks on futex, another ready -> switch
// ============================================================================

func TestFutexBlockSwitchToReady(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running (will block)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	_ = threadA
	env.setCurrentThread(0)

	// Thread B ready
	threadB := env.createThread(1, ThreadId(200), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(200))

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000, 1001})

	t.Log("Scenario: Thread A blocks on futex, Thread B ready -> switch to B")

	futexAddr := uint64(0x12345678)
	result := ThreadBlockFutex(sf, futexAddr)

	if result == 0 {
		t.Error("Expected switch to B, got 0")
	}

	// A should be blocked
	if env.threadListData[0].State != ThreadBlockedFutex {
		t.Errorf("Thread A should be BlockedFutex, got %d", env.threadListData[0].State)
	}
	if env.threadListData[0].FutexAddr != futexAddr {
		t.Errorf("Thread A futex addr should be 0x%x, got 0x%x", futexAddr, env.threadListData[0].FutexAddr)
	}

	// B should be running
	if env.threadListData[1].State != ThreadRunning {
		t.Errorf("Thread B should be Running, got %d", env.threadListData[1].State)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Futex block switches to ready thread")
}

// ============================================================================
// Test 9: Blocked thread woken -> becomes ready
// ============================================================================

func TestFutexWakeBecomesReady(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	_ = threadA
	env.setCurrentThread(0)

	// Thread B blocked on futex
	threadB := env.createThread(1, ThreadId(200), ThreadBlockedFutex)
	threadB.FutexAddr = 0x12345678
	blockedQueue.Push(ThreadId(200))

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000})

	t.Log("Scenario: Thread B blocked on futex, wake it -> becomes ready")

	woken := ThreadWakeFutex(sf, 0x12345678, 1)

	if woken != 1 {
		t.Errorf("Expected 1 thread woken, got %d", woken)
	}

	// B should be ready now
	if env.threadListData[1].State != ThreadReady {
		t.Errorf("Thread B should be Ready after wake, got %d", env.threadListData[1].State)
	}

	// B should be in ready queue
	found := false
	for i := 0; i < len(env.readyQueueData); i++ {
		if env.readyQueueInUse[i] && env.readyQueueData[i] == ThreadId(200) {
			found = true
			break
		}
	}
	if !found {
		t.Error("Thread B should be in ready queue after wake")
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Futex wake makes thread ready")
}

// ============================================================================
// Test 10: Multiple threads blocked on futex, wake one
// ============================================================================

func TestFutexWakeOne(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running
	env.createThread(0, ThreadId(100), ThreadRunning)
	env.setCurrentThread(0)

	// Threads B, C, D blocked on same futex
	futexAddr := uint64(0x12345678)
	for i, tid := range []ThreadId{200, 300, 400} {
		th := env.createThread(i+1, tid, ThreadBlockedFutex)
		th.FutexAddr = futexAddr
		blockedQueue.Push(tid)
	}

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000})

	t.Log("Scenario: 3 threads blocked on futex, wake 1 -> only 1 becomes ready")

	woken := ThreadWakeFutex(sf, futexAddr, 1)

	if woken != 1 {
		t.Errorf("Expected 1 thread woken, got %d", woken)
	}

	// Count how many are now ready
	readyCount := 0
	blockedCount := 0
	for i := 1; i <= 3; i++ {
		if env.threadListData[i].State == ThreadReady {
			readyCount++
		} else if env.threadListData[i].State == ThreadBlockedFutex {
			blockedCount++
		}
	}

	if readyCount != 1 {
		t.Errorf("Expected 1 ready thread, got %d", readyCount)
	}
	if blockedCount != 2 {
		t.Errorf("Expected 2 still blocked, got %d", blockedCount)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Futex wake(1) wakes exactly one thread")
}

// ============================================================================
// Test 11: Multiple threads blocked, wake_all
// ============================================================================

func TestFutexWakeAll(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running
	env.createThread(0, ThreadId(100), ThreadRunning)
	env.setCurrentThread(0)

	// Threads B, C, D blocked on same futex
	futexAddr := uint64(0x12345678)
	for i, tid := range []ThreadId{200, 300, 400} {
		th := env.createThread(i+1, tid, ThreadBlockedFutex)
		th.FutexAddr = futexAddr
		blockedQueue.Push(tid)
	}

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000})

	t.Log("Scenario: 3 threads blocked on futex, wake all")

	// Wake with maxWake = 100 (more than blocked)
	woken := ThreadWakeFutex(sf, futexAddr, 100)

	if woken != 3 {
		t.Errorf("Expected 3 threads woken, got %d", woken)
	}

	// All should be ready
	for i := 1; i <= 3; i++ {
		if env.threadListData[i].State != ThreadReady {
			t.Errorf("Thread %d should be Ready, got %d", i, env.threadListData[i].State)
		}
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Futex wake(N) wakes all blocked threads")
}

// ============================================================================
// Test 15: FIFO ordering in ready queue
// ============================================================================

func TestReadyQueueFIFO(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Create threads and add to ready queue in order: B, C, D
	env.createThread(1, ThreadId(200), ThreadReady)
	env.createThread(2, ThreadId(300), ThreadReady)
	env.createThread(3, ThreadId(400), ThreadReady)
	readyQueue.Push(ThreadId(200))
	readyQueue.Push(ThreadId(300))
	readyQueue.Push(ThreadId(400))

	// Thread A running
	env.createThread(0, ThreadId(100), ThreadRunning)
	env.setCurrentThread(0)

	t.Log("Scenario: Ready queue has B, C, D (in that order). Verify FIFO.")

	// Pop should give us 200 first (FIFO)
	tid1 := readyQueue.Pop()
	tid2 := readyQueue.Pop()
	tid3 := readyQueue.Pop()

	if tid1 != ThreadId(200) {
		t.Errorf("First pop should be 200, got %d", tid1)
	}
	if tid2 != ThreadId(300) {
		t.Errorf("Second pop should be 300, got %d", tid2)
	}
	if tid3 != ThreadId(400) {
		t.Errorf("Third pop should be 400, got %d", tid3)
	}

	t.Log("PASS: Ready queue maintains FIFO order")
}

// ============================================================================
// Test 18: Nested scenario: A preempted -> B runs -> B blocks -> back to A
// ============================================================================

func TestNestedPreemptBlockReturn(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	threadA.StartTick = 1000
	env.setCurrentThread(0)

	// Thread B ready
	threadB := env.createThread(1, ThreadId(200), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(200))

	// Times for: preemption check, B's start, B's block
	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{10000000, 10000001, 10000002, 10000003})

	t.Log("Scenario: A running -> preempted to B -> B blocks -> back to A")

	// Step 1: Preempt A to B
	t.Log("Step 1: Preempt A")
	result1 := checkThreadPreemptionInternal(sf, 0)
	if result1 == 0 {
		t.Fatal("Expected switch to B")
	}

	// Verify B is now running
	if env.threadListData[1].State != ThreadRunning {
		t.Fatalf("B should be Running, got %d", env.threadListData[1].State)
	}
	if env.threadListData[0].State != ThreadReady {
		t.Fatalf("A should be Ready, got %d", env.threadListData[0].State)
	}
	t.Log("  A preempted, B running")

	// Step 2: B blocks on futex
	t.Log("Step 2: B blocks on futex")
	futexAddr := uint64(0xDEADBEEF)
	result2 := ThreadBlockFutex(sf, futexAddr)
	if result2 == 0 {
		t.Fatal("Expected switch back to A")
	}

	// Verify A is running again
	if env.threadListData[0].State != ThreadRunning {
		t.Errorf("A should be Running again, got %d", env.threadListData[0].State)
	}
	if env.threadListData[1].State != ThreadBlockedFutex {
		t.Errorf("B should be BlockedFutex, got %d", env.threadListData[1].State)
	}

	current := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if current.TID != ThreadId(100) {
		t.Errorf("CurrentThread should be A (100), got %d", current.TID)
	}

	t.Log("  B blocked, A running again")

	checkInvariants(t, daifPair)
	t.Log("PASS: Nested preempt->block->return works correctly")
}

// ============================================================================
// Test 12: Thread sleeps, wakes after deadline (basic sleep test)
// ============================================================================

func TestThreadSleepBasic(t *testing.T) {
	env := newSchedulerTestEnv()
	defer env.restore()

	// Thread A running (will sleep)
	threadA := env.createThread(0, ThreadId(100), ThreadRunning)
	_ = threadA
	env.setCurrentThread(0)

	// Thread B ready
	threadB := env.createThread(1, ThreadId(200), ThreadReady)
	_ = threadB
	readyQueue.Push(ThreadId(200))

	sf, daifPair, _ := newTestSchedulerFunc(t, []uint64{1000, 1001})

	t.Log("Scenario: Thread A sleeps, Thread B ready -> switch to B")

	result := ThreadBlockSleep(sf)

	if result == 0 {
		t.Error("Expected switch to B, got 0")
	}

	// A should be sleeping
	if env.threadListData[0].State != ThreadSleeping {
		t.Errorf("Thread A should be Sleeping, got %d", env.threadListData[0].State)
	}

	// B should be running
	if env.threadListData[1].State != ThreadRunning {
		t.Errorf("Thread B should be Running, got %d", env.threadListData[1].State)
	}

	checkInvariants(t, daifPair)
	t.Log("PASS: Sleep blocks and switches to ready thread")
}
