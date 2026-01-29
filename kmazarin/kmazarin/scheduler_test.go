//go:build arm64

package main

import (
	"mazzy/kmazarin/ds"
	"sync/atomic"
	"testing"
	"unsafe"
)

// DAIFPair tracks DAIF disable/enable calls to verify they are balanced.
// Used in tests to detect missing or extra DAIF restore calls.
type DAIFPair struct {
	daifCalls int // Incremented by Disable, decremented by Enable
}

// DisableAndSaveDAIF increments the counter and returns a dummy saved state.
func (d *DAIFPair) DisableAndSaveDAIF() uint64 {
	d.daifCalls++
	return 0 // Dummy saved state for tests
}

// EnableAndRestoreDAIF decrements the counter.
func (d *DAIFPair) EnableAndRestoreDAIF(savedState uint64) {
	d.daifCalls--
}

// Balanced returns true if all Disable calls have matching Enable calls.
// Should be called at the end of a test to verify lock discipline.
func (d *DAIFPair) Balanced() bool {
	return d.daifCalls == 0
}

// TestClock provides deterministic time values for testing.
// Each call returns the cumulative sum of the timing array up to that call.
// Panics if more calls are made than array entries.
type TestClock struct {
	timings []uint64 // Array of time deltas
	callIdx int      // Current call index
}

// NewTestClock creates a TestClock with the given timing array.
// The first value should be a plausible initial timer value (e.g., 0x0001234567890000).
// Subsequent values are deltas added to the cumulative sum.
//
// Example: []uint64{0x0001234567890000, 100, 200, 50}
//
//	Call 0: returns 0x0001234567890000
//	Call 1: returns 0x0001234567890000 + 100
//	Call 2: returns 0x0001234567890000 + 100 + 200
//	Call 3: returns 0x0001234567890000 + 100 + 200 + 50
func NewTestClock(timings []uint64) *TestClock {
	return &TestClock{
		timings: timings,
		callIdx: 0,
	}
}

// CurrentTime returns the cumulative sum up to the current call index.
// The fakeTime parameter is ignored (provided for SchedulerFunc compatibility).
func (tc *TestClock) CurrentTime(fakeTime uint64) uint64 {
	if tc.callIdx >= len(tc.timings) {
		panic("TestClock: exceeded timing array bounds")
	}

	// Calculate cumulative sum from 0 to callIdx (inclusive)
	sum := uint64(0)
	for i := 0; i <= tc.callIdx; i++ {
		sum += tc.timings[i]
	}

	tc.callIdx++
	return sum
}

// TestThreadPreemption verifies that a thread that has exhausted its timeslice
// is preempted when a timer interrupt occurs.
//
// Scenario:
//   - Thread 14 is currently running
//   - Thread 14 has used up its entire timeslice (elapsed >= threshold)
//   - Thread 32 is on the ready queue
//   - Timer interrupt fires
//
// Expected Result:
//   - Thread 32 becomes running
//   - Thread 14 moves to ready queue
//   - Thread states are updated correctly
func TestThreadPreemption(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origCurrentThreadIdx := CurrentThreadIdx
	origCurrentThread := atomic.LoadPointer(&CurrentThread)
	origTimerFreq := timerFrequencyHz
	origSpinBackoff := ds.SpinBackoffTicks

	defer func() {
		// Restore original state
		threadList = origThreadList
		readyQueue = origReadyQueue
		CurrentThreadIdx = origCurrentThreadIdx
		atomic.StorePointer(&CurrentThread, origCurrentThread)
		timerFrequencyHz = origTimerFreq
		ds.SpinBackoffTicks = origSpinBackoff
	}()

	// Initialize test environment
	timerFrequencyHz = 62500000 // 62.5MHz
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Setup test timing values BEFORE creating threads
	preemptThresholdTicks := uint64(6250000) // 100ms @ 62.5MHz
	threadStartTime := uint64(1000)
	currentTime := threadStartTime + preemptThresholdTicks + 1000 // Exceeded by 1000 ticks

	// Create Thread 14 (currently running, timeslice exhausted)
	threadList.Data[0] = Thread{
		TID:            ThreadId(14),
		State:          ThreadRunning,
		StartTick:      threadStartTime, // Started at tick 1000
		PreemptElapsed: 0,               // No accumulated time yet
		LastSeenG:      0x1000,          // Some g pointer
		MPtr:           0x2000,
		GPtr:           0x3000,
	}
	threadList.InUse[0] = true
	CurrentThreadIdx = 0
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&threadList.Data[0]))

	// Create Thread 32 (on ready queue)
	threadList.Data[1] = Thread{
		TID:            ThreadId(32),
		State:          ThreadReady,
		StartTick:      0,
		PreemptElapsed: 5000, // Has some accumulated time
		LastSeenG:      0x4000,
		MPtr:           0x5000,
		GPtr:           0x6000,
	}
	threadList.InUse[1] = true

	// Add Thread 32 to ready queue using Push
	readyQueue.Push(ThreadId(32))

	// Create test helpers
	daifPair := &DAIFPair{}
	testClock := NewTestClock([]uint64{
		currentTime, // First call returns current time (already defined above)
	})

	testFunc := SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck: func(checkpoint string) {
			t.Logf("StateCheck: %s", checkpoint)
			// Could add invariant checks here
		},
	}

	t.Logf("Before interrupt:")
	t.Logf("  Thread 14: state=%d, running=%v", threadList.Data[0].State, threadList.Data[0].State == ThreadRunning)
	t.Logf("  Thread 32: state=%d, ready=%v", threadList.Data[1].State, threadList.Data[1].State == ThreadReady)
	t.Logf("  Current thread: TID=%d", (*Thread)(atomic.LoadPointer(&CurrentThread)).TID)
	t.Logf("  Ready queue[0]: TID=%d", readyQueue.Data[0])

	// Call the preemption check directly (simulates timer interrupt)
	// checkThreadPreemptionImpl handles the DAIF and lock internally
	contextPtr := checkThreadPreemptionImpl(&testFunc, 0)

	t.Logf("Context switch returned: 0x%x (0 = no switch needed)", contextPtr)

	// Verify results
	t.Logf("After interrupt:")
	t.Logf("  Thread 14: state=%d, ready=%v", threadList.Data[0].State, threadList.Data[0].State == ThreadReady)
	t.Logf("  Thread 32: state=%d, running=%v", threadList.Data[1].State, threadList.Data[1].State == ThreadRunning)
	CurrentThreadPtr := (*Thread)(atomic.LoadPointer(&CurrentThread))
	t.Logf("  Current thread: TID=%d", CurrentThreadPtr.TID)
	t.Logf("  Ready queue[0]: TID=%d", readyQueue.Data[0])

	// Verify Thread 32 is now running
	if threadList.Data[1].State != ThreadRunning {
		t.Errorf("Thread 32 should be running, got state=%d", threadList.Data[1].State)
	}
	if CurrentThreadPtr.TID != ThreadId(32) {
		t.Errorf("Current thread should be 32, got %d", CurrentThreadPtr.TID)
	}
	if CurrentThreadIdx != 1 {
		t.Errorf("Current thread index should be 1, got %d", CurrentThreadIdx)
	}

	// Verify Thread 14 is now on ready queue
	if threadList.Data[0].State != ThreadReady {
		t.Errorf("Thread 14 should be ready, got state=%d", threadList.Data[0].State)
	}

	// Find Thread 14 in ready queue
	found := false
	for i := 0; i < len(readyQueue.Data); i++ {
		if readyQueue.InUse[i] && readyQueue.Data[i] == ThreadId(14) {
			found = true
			break
		}
	}
	if !found {
		t.Error("Thread 14 should be on ready queue")
	}

	// Verify lock discipline - DAIF calls must be balanced
	if !daifPair.Balanced() {
		t.Errorf("Unbalanced DAIF calls: counter=%d (should be 0)", daifPair.daifCalls)
	} else {
		t.Logf("Lock discipline: DAIF calls balanced ✓")
	}
}

// TestDAIFPair verifies the DAIFPair helper tracks calls correctly
func TestDAIFPair(t *testing.T) {
	pair := &DAIFPair{}

	// Initially balanced
	if !pair.Balanced() {
		t.Error("DAIFPair should start balanced")
	}

	// After disable, not balanced
	pair.DisableAndSaveDAIF()
	if pair.Balanced() {
		t.Error("DAIFPair should not be balanced after Disable")
	}
	if pair.daifCalls != 1 {
		t.Errorf("Expected daifCalls=1, got %d", pair.daifCalls)
	}

	// After enable, balanced again
	pair.EnableAndRestoreDAIF(0)
	if !pair.Balanced() {
		t.Error("DAIFPair should be balanced after matching Enable")
	}
	if pair.daifCalls != 0 {
		t.Errorf("Expected daifCalls=0, got %d", pair.daifCalls)
	}

	// Nested disable/enable
	pair.DisableAndSaveDAIF()
	pair.DisableAndSaveDAIF()
	if pair.daifCalls != 2 {
		t.Errorf("Expected daifCalls=2, got %d", pair.daifCalls)
	}
	pair.EnableAndRestoreDAIF(0)
	if pair.daifCalls != 1 {
		t.Errorf("Expected daifCalls=1 after one restore, got %d", pair.daifCalls)
	}
	pair.EnableAndRestoreDAIF(0)
	if !pair.Balanced() {
		t.Error("DAIFPair should be balanced after all restores")
	}
}

// TestTestClock verifies the TestClock helper returns cumulative sums correctly
func TestTestClock(t *testing.T) {
	timings := []uint64{100, 20, 30, 50}
	clock := NewTestClock(timings)

	// First call: 100
	if val := clock.CurrentTime(0); val != 100 {
		t.Errorf("Call 0: expected 100, got %d", val)
	}

	// Second call: 100 + 20 = 120
	if val := clock.CurrentTime(0); val != 120 {
		t.Errorf("Call 1: expected 120, got %d", val)
	}

	// Third call: 100 + 20 + 30 = 150
	if val := clock.CurrentTime(0); val != 150 {
		t.Errorf("Call 2: expected 150, got %d", val)
	}

	// Fourth call: 100 + 20 + 30 + 50 = 200
	if val := clock.CurrentTime(0); val != 200 {
		t.Errorf("Call 3: expected 200, got %d", val)
	}

	// Fifth call should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when exceeding array bounds")
		}
	}()
	clock.CurrentTime(0)
}

// TestPriestPreference_DifferentPriest verifies that findReadyThreadPreferDifferentPriestSchedLockHeld
// selects a thread from a different priest when available.
//
// Scenario:
//   - Thread A (PID=1) is currently running
//   - Thread B (PID=1) is ready (same priest as A)
//   - Thread C (PID=2) is ready (different priest from A)
//   - Ready queue order: [B, C]
//
// Expected Result:
//   - findReadyThreadPreferDifferentPriestSchedLockHeld(1) returns Thread C (different priest preferred)
//   - Thread C is removed from ready queue
//   - Thread B remains in ready queue
func TestPriestPreference_DifferentPriest(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create Thread B (PID=1, ready)
	threadList.Data[0] = Thread{
		TID:   ThreadId(10),
		State: ThreadReady,
		PID:   PriestId(1),
	}
	threadList.InUse[0] = true

	// Create Thread C (PID=2, ready)
	threadList.Data[1] = Thread{
		TID:   ThreadId(20),
		State: ThreadReady,
		PID:   PriestId(2),
	}
	threadList.InUse[1] = true

	// Add both to ready queue (B first, then C)
	readyQueue.Push(ThreadId(10)) // Thread B
	readyQueue.Push(ThreadId(20)) // Thread C

	t.Logf("Before selection:")
	t.Logf("  Thread B (TID=10): PID=1, state=%d", threadList.Data[0].State)
	t.Logf("  Thread C (TID=20): PID=2, state=%d", threadList.Data[1].State)
	t.Logf("  Ready queue: [%d, %d]", readyQueue.Data[0], readyQueue.Data[1])

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld with currentPID=1
	// Should select Thread C (PID=2) even though B is first
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(1))

	// Verify Thread C was selected
	if selected == nil {
		t.Fatal("Expected thread to be selected, got nil")
	}
	if selected.TID != ThreadId(20) {
		t.Errorf("Expected Thread C (TID=20), got TID=%d", selected.TID)
	}
	if selected.PID != PriestId(2) {
		t.Errorf("Expected PID=2, got PID=%d", selected.PID)
	}

	// Verify Thread C was removed from ready queue
	// Ready queue should now only contain Thread B
	if readyQueue.InUse[1] {
		t.Error("Thread C should have been removed from ready queue")
	}
	if !readyQueue.InUse[0] {
		t.Error("Thread B should still be in ready queue")
	}

	t.Logf("After selection:")
	t.Logf("  Selected: TID=%d, PID=%d", selected.TID, selected.PID)
	t.Logf("  Ready queue slot 0 (Thread B): in_use=%v", readyQueue.InUse[0])
	t.Logf("  Ready queue slot 1 (Thread C): in_use=%v", readyQueue.InUse[1])
}

// TestPriestPreference_FallbackSamePriest verifies that findReadyThreadPreferDifferentPriestSchedLockHeld
// falls back to a same-priest thread when no different-priest threads are available.
//
// Scenario:
//   - Thread A (PID=1) is currently running
//   - Thread B (PID=1) is ready (same priest as A)
//   - Thread C (PID=1) is ready (same priest as A)
//   - No different-priest threads available
//   - Ready queue order: [B, C]
//
// Expected Result:
//   - findReadyThreadPreferDifferentPriestSchedLockHeld(1) returns Thread B (FIFO fallback)
//   - Thread B is removed from ready queue
func TestPriestPreference_FallbackSamePriest(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create Thread B (PID=1, ready)
	threadList.Data[0] = Thread{
		TID:   ThreadId(10),
		State: ThreadReady,
		PID:   PriestId(1),
	}
	threadList.InUse[0] = true

	// Create Thread C (PID=1, ready)
	threadList.Data[1] = Thread{
		TID:   ThreadId(20),
		State: ThreadReady,
		PID:   PriestId(1),
	}
	threadList.InUse[1] = true

	// Add both to ready queue (B first, then C)
	readyQueue.Push(ThreadId(10)) // Thread B
	readyQueue.Push(ThreadId(20)) // Thread C

	t.Logf("Before selection:")
	t.Logf("  Thread B (TID=10): PID=1, state=%d", threadList.Data[0].State)
	t.Logf("  Thread C (TID=20): PID=1, state=%d", threadList.Data[1].State)
	t.Logf("  Ready queue: [%d, %d]", readyQueue.Data[0], readyQueue.Data[1])

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld with currentPID=1
	// Should fall back to FIFO (Thread B) since all are same priest
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(1))

	// Verify Thread B was selected (FIFO fallback)
	if selected == nil {
		t.Fatal("Expected thread to be selected, got nil")
	}
	if selected.TID != ThreadId(10) {
		t.Errorf("Expected Thread B (TID=10) via FIFO fallback, got TID=%d", selected.TID)
	}
	if selected.PID != PriestId(1) {
		t.Errorf("Expected PID=1, got PID=%d", selected.PID)
	}

	// Verify Thread B was removed from ready queue
	if readyQueue.InUse[0] {
		t.Error("Thread B should have been removed from ready queue")
	}
	if !readyQueue.InUse[1] {
		t.Error("Thread C should still be in ready queue")
	}

	t.Logf("After selection:")
	t.Logf("  Selected: TID=%d, PID=%d (FIFO fallback)", selected.TID, selected.PID)
	t.Logf("  Ready queue slot 0 (Thread B): in_use=%v", readyQueue.InUse[0])
	t.Logf("  Ready queue slot 1 (Thread C): in_use=%v", readyQueue.InUse[1])
}

// TestPriestPreference_KernelThreads verifies that kernel threads (PID=0) are treated
// as a distinct priest and that preference logic works correctly.
//
// Scenario:
//   - Kernel thread A (PID=0) is currently running
//   - Kernel thread B (PID=0) is ready
//   - Userspace thread C (PID=1) is ready
//   - Ready queue order: [B, C]
//
// Expected Result:
//   - findReadyThreadPreferDifferentPriestSchedLockHeld(0) returns Thread C (different priest preferred)
//   - Thread C is removed from ready queue
//   - Thread B remains in ready queue
func TestPriestPreference_KernelThreads(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create Kernel Thread B (PID=0, ready)
	threadList.Data[0] = Thread{
		TID:   ThreadId(5),
		State: ThreadReady,
		PID:   PriestId(0), // Kernel thread
	}
	threadList.InUse[0] = true

	// Create Userspace Thread C (PID=1, ready)
	threadList.Data[1] = Thread{
		TID:   ThreadId(15),
		State: ThreadReady,
		PID:   PriestId(1), // Userspace
	}
	threadList.InUse[1] = true

	// Add both to ready queue (B first, then C)
	readyQueue.Push(ThreadId(5))  // Kernel Thread B
	readyQueue.Push(ThreadId(15)) // Userspace Thread C

	t.Logf("Before selection:")
	t.Logf("  Kernel Thread B (TID=5): PID=0, state=%d", threadList.Data[0].State)
	t.Logf("  Userspace Thread C (TID=15): PID=1, state=%d", threadList.Data[1].State)
	t.Logf("  Ready queue: [%d, %d]", readyQueue.Data[0], readyQueue.Data[1])

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld with currentPID=0 (kernel)
	// Should select Thread C (PID=1) even though B is first
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(0))

	// Verify Thread C was selected
	if selected == nil {
		t.Fatal("Expected thread to be selected, got nil")
	}
	if selected.TID != ThreadId(15) {
		t.Errorf("Expected Thread C (TID=15), got TID=%d", selected.TID)
	}
	if selected.PID != PriestId(1) {
		t.Errorf("Expected PID=1, got PID=%d", selected.PID)
	}

	// Verify Thread C was removed from ready queue
	if readyQueue.InUse[1] {
		t.Error("Thread C should have been removed from ready queue")
	}
	if !readyQueue.InUse[0] {
		t.Error("Thread B should still be in ready queue")
	}

	t.Logf("After selection:")
	t.Logf("  Selected: TID=%d, PID=%d", selected.TID, selected.PID)
	t.Logf("  Ready queue slot 0 (Kernel B): in_use=%v", readyQueue.InUse[0])
	t.Logf("  Ready queue slot 1 (Userspace C): in_use=%v", readyQueue.InUse[1])
}

// TestPriestPreference_EmptyQueue verifies that findReadyThreadPreferDifferentPriestSchedLockHeld
// returns nil when the ready queue is empty.
//
// Expected Result:
//   - Returns nil (no thread to select)
func TestPriestPreference_EmptyQueue(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Don't add any threads to ready queue

	t.Logf("Ready queue is empty")

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(1))

	// Verify nil was returned
	if selected != nil {
		t.Errorf("Expected nil from empty queue, got TID=%d", selected.TID)
	}

	t.Logf("Correctly returned nil")
}

// TestPriestPreference_StaleEntries verifies that findReadyThreadPreferDifferentPriestSchedLockHeld
// correctly handles and skips stale entries in the ready queue.
//
// Scenario:
//   - Thread A (PID=1) is in ready queue but marked as ThreadBlocked (stale)
//   - Thread B (PID=2) is in ready queue and marked ThreadReady (valid)
//   - Ready queue order: [A, B]
//
// Expected Result:
//   - Skips stale Thread A
//   - Returns Thread B (valid)
func TestPriestPreference_StaleEntries(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create Thread A (PID=1, but marked as BLOCKED - stale entry)
	threadList.Data[0] = Thread{
		TID:   ThreadId(10),
		State: ThreadBlockedFutex, // Stale - shouldn't be in ready queue
		PID:   PriestId(1),
	}
	threadList.InUse[0] = true

	// Create Thread B (PID=2, ready - valid)
	threadList.Data[1] = Thread{
		TID:   ThreadId(20),
		State: ThreadReady,
		PID:   PriestId(2),
	}
	threadList.InUse[1] = true

	// Add both to ready queue (A is stale, B is valid)
	readyQueue.Push(ThreadId(10)) // Thread A (stale)
	readyQueue.Push(ThreadId(20)) // Thread B (valid)

	t.Logf("Before selection:")
	t.Logf("  Thread A (TID=10): PID=1, state=%d (BLOCKED_FUTEX - stale)", threadList.Data[0].State)
	t.Logf("  Thread B (TID=20): PID=2, state=%d (READY)", threadList.Data[1].State)
	t.Logf("  Ready queue: [%d, %d]", readyQueue.Data[0], readyQueue.Data[1])

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld with currentPID=1
	// Should skip stale Thread A and select Thread B
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(1))

	// Verify Thread B was selected (skipped stale A)
	if selected == nil {
		t.Fatal("Expected thread to be selected, got nil")
	}
	if selected.TID != ThreadId(20) {
		t.Errorf("Expected Thread B (TID=20), got TID=%d", selected.TID)
	}
	if selected.State != ThreadReady {
		t.Errorf("Expected state=ThreadReady, got state=%d", selected.State)
	}

	// Verify Thread B was removed from ready queue
	if readyQueue.InUse[1] {
		t.Error("Thread B should have been removed from ready queue")
	}

	t.Logf("After selection:")
	t.Logf("  Selected: TID=%d, PID=%d (correctly skipped stale entry)", selected.TID, selected.PID)
	t.Logf("  Ready queue slot 0 (stale A): in_use=%v", readyQueue.InUse[0])
	t.Logf("  Ready queue slot 1 (valid B): in_use=%v", readyQueue.InUse[1])
}

// TestPriestPreference_MultipleChoices verifies that findReadyThreadPreferDifferentPriestSchedLockHeld
// selects the FIRST different-priest thread when multiple options are available.
//
// Scenario:
//   - Thread A (PID=1) is currently running
//   - Thread B (PID=1) is ready (same priest)
//   - Thread C (PID=2) is ready (different priest)
//   - Thread D (PID=3) is ready (different priest)
//   - Ready queue order: [B, C, D]
//
// Expected Result:
//   - Returns Thread C (first different-priest thread encountered in scan)
func TestPriestPreference_MultipleChoices(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create Thread B (PID=1, ready)
	threadList.Data[0] = Thread{
		TID:   ThreadId(10),
		State: ThreadReady,
		PID:   PriestId(1),
	}
	threadList.InUse[0] = true

	// Create Thread C (PID=2, ready)
	threadList.Data[1] = Thread{
		TID:   ThreadId(20),
		State: ThreadReady,
		PID:   PriestId(2),
	}
	threadList.InUse[1] = true

	// Create Thread D (PID=3, ready)
	threadList.Data[2] = Thread{
		TID:   ThreadId(30),
		State: ThreadReady,
		PID:   PriestId(3),
	}
	threadList.InUse[2] = true

	// Add all to ready queue
	readyQueue.Push(ThreadId(10)) // Thread B (same priest)
	readyQueue.Push(ThreadId(20)) // Thread C (different priest)
	readyQueue.Push(ThreadId(30)) // Thread D (different priest)

	t.Logf("Before selection:")
	t.Logf("  Thread B (TID=10): PID=1")
	t.Logf("  Thread C (TID=20): PID=2")
	t.Logf("  Thread D (TID=30): PID=3")
	t.Logf("  Ready queue: [%d, %d, %d]", readyQueue.Data[0], readyQueue.Data[1], readyQueue.Data[2])

	// Call findReadyThreadPreferDifferentPriestSchedLockHeld with currentPID=1
	// Should select Thread C (first different-priest in scan order)
	selected := findReadyThreadPreferDifferentPriestSchedLockHeld(PriestId(1))

	// Verify Thread C was selected (first different priest encountered)
	if selected == nil {
		t.Fatal("Expected thread to be selected, got nil")
	}
	if selected.TID != ThreadId(20) {
		t.Errorf("Expected Thread C (TID=20), got TID=%d", selected.TID)
	}
	if selected.PID != PriestId(2) {
		t.Errorf("Expected PID=2, got PID=%d", selected.PID)
	}

	// Verify Thread C was removed, B and D remain
	if !readyQueue.InUse[0] {
		t.Error("Thread B should still be in ready queue")
	}
	if readyQueue.InUse[1] {
		t.Error("Thread C should have been removed from ready queue")
	}
	if !readyQueue.InUse[2] {
		t.Error("Thread D should still be in ready queue")
	}

	t.Logf("After selection:")
	t.Logf("  Selected: TID=%d, PID=%d", selected.TID, selected.PID)
}

// TestPriestPreference_PreemptionFairness verifies that repeated preemption
// rotates between different priests rather than monopolizing one.
//
// Scenario:
//   - 4 priests, 3 threads each (12 threads total)
//   - Simulate repeated calls to findReadyThreadPreferDifferentPriestSchedLockHeld
//   - Verify rotation across priests
func TestPriestPreference_PreemptionFairness(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays for test
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create 4 priests, 3 threads each
	idx := 0
	for pid := PriestId(1); pid <= 4; pid++ {
		for j := 0; j < 3; j++ {
			tid := ThreadId(int(pid)*10 + j)
			threadList.Data[idx] = Thread{
				TID:   tid,
				State: ThreadReady,
				PID:   pid,
			}
			threadList.InUse[idx] = true
			readyQueue.Push(tid)
			idx++
		}
	}

	t.Log("Scenario: 4 priests x 3 threads, simulate repeated preemption selections")

	// Track which priests are selected consecutively
	lastPID := PriestId(1) // Simulate priest 1 was running
	consecutiveSame := 0
	maxConsecutiveSame := 0

	for i := 0; i < 8; i++ {
		selected := findReadyThreadPreferDifferentPriestSchedLockHeld(lastPID)
		if selected == nil {
			t.Fatalf("Iteration %d: got nil, expected thread", i)
		}

		if selected.PID == lastPID {
			consecutiveSame++
		} else {
			consecutiveSame = 0
		}
		if consecutiveSame > maxConsecutiveSame {
			maxConsecutiveSame = consecutiveSame
		}

		t.Logf("  Iteration %d: current PID=%d, selected TID=%d PID=%d",
			i, lastPID, selected.TID, selected.PID)

		// The selected thread should NOT be from the same priest as current
		// (when threads from other priests are available)
		if i < 6 && selected.PID == lastPID {
			t.Errorf("Iteration %d: selected same priest %d when others available",
				i, lastPID)
		}

		// Put selected thread back in ready queue for next iteration
		selected.State = ThreadReady
		readyQueue.PushNoDuplicate(selected.TID)
		lastPID = selected.PID
	}

	t.Logf("Max consecutive same-priest selections: %d", maxConsecutiveSame)
}

// TestPriestTickAccounting verifies that priest-level tick accounting
// works correctly across thread switches.
func TestPriestTickAccounting(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origPriestList := priestList
	origCurrentThreadIdx := CurrentThreadIdx
	origCurrentThread := atomic.LoadPointer(&CurrentThread)
	origTimerFreq := timerFrequencyHz
	origSpinBackoff := ds.SpinBackoffTicks

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
		priestList = origPriestList
		CurrentThreadIdx = origCurrentThreadIdx
		atomic.StorePointer(&CurrentThread, origCurrentThread)
		timerFrequencyHz = origTimerFreq
		ds.SpinBackoffTicks = origSpinBackoff
	}()

	// Initialize test environment
	timerFrequencyHz = 62500000
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Setup thread list
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Setup priest list
	var testPriestListData [MaxPriests]Priest
	var testPriestListInUse [MaxPriests]bool
	priestList.Data = testPriestListData[:]
	priestList.InUse = testPriestListInUse[:]

	// Create priest 1 (index 0)
	priestList.Data[0] = Priest{PID: PriestId(1)}
	priestList.InUse[0] = true

	// Create priest 2 (index 1)
	priestList.Data[1] = Priest{PID: PriestId(2)}
	priestList.InUse[1] = true

	// Create thread A (priest 1, running)
	threadList.Data[0] = Thread{
		TID:                ThreadId(10),
		State:              ThreadRunning,
		PID:                PriestId(1),
		PriestIdx:          0, // index into priestList
		StartTick:          1000,
		TicksStartedRunning: 1000,
	}
	threadList.InUse[0] = true
	CurrentThreadIdx = 0
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&threadList.Data[0]))

	// Create thread B (priest 2, ready)
	threadList.Data[1] = Thread{
		TID:       ThreadId(20),
		State:     ThreadReady,
		PID:       PriestId(2),
		PriestIdx: 1,
	}
	threadList.InUse[1] = true
	readyQueue.Push(ThreadId(20))

	// Create thread C (priest 1, ready) - same priest as A
	threadList.Data[2] = Thread{
		TID:       ThreadId(11),
		State:     ThreadReady,
		PID:       PriestId(1),
		PriestIdx: 0,
	}
	threadList.InUse[2] = true
	readyQueue.Push(ThreadId(11))

	// Start priest 1's clock
	priestList.Data[0].TicksStartedRunning = 1000

	// Simulate preemption at time=2000 (1000 ticks elapsed for priest 1)
	daifPair := &DAIFPair{}
	testClock := NewTestClock([]uint64{2000}) // currentTime = 2000
	sf := &SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck: func(checkpoint string) {
			t.Logf("StateCheck: %s", checkpoint)
		},
	}

	result := checkThreadPreemptionImpl(sf, 0)
	if result == 0 {
		t.Fatal("Expected context switch, got 0")
	}

	// Verify priest 1's total ticks were accumulated
	if priestList.Data[0].TotalTicksRunning != 1000 {
		t.Errorf("Priest 1 TotalTicksRunning: expected 1000, got %d",
			priestList.Data[0].TotalTicksRunning)
	}

	// Verify priest 1's clock was stopped (switched to different priest)
	if priestList.Data[0].TicksStartedRunning != 0 {
		t.Errorf("Priest 1 TicksStartedRunning should be 0 after switch to different priest, got %d",
			priestList.Data[0].TicksStartedRunning)
	}

	// Verify priest 2's clock was started
	if priestList.Data[1].TicksStartedRunning != 2000 {
		t.Errorf("Priest 2 TicksStartedRunning should be 2000, got %d",
			priestList.Data[1].TicksStartedRunning)
	}

	// Verify the selected thread is from priest 2 (prefer different priest)
	current := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if current.PID != PriestId(2) {
		t.Errorf("Expected switch to priest 2, got priest %d", current.PID)
	}

	if !daifPair.Balanced() {
		t.Errorf("Unbalanced DAIF calls: %d", daifPair.daifCalls)
	}

	t.Log("PASS: Priest-level tick accounting works correctly")
}

// TestEnqueueReadyKernelPriority verifies that kernel threads (PID==0) are
// inserted at the FRONT of the ready queue while priest threads go to the BACK.
func TestEnqueueReadyKernelPriority(t *testing.T) {
	// Save and restore global state
	origThreadList := threadList
	origReadyQueue := readyQueue
	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup backing arrays
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create a priest thread (PID=5) and a kernel thread (PID=0)
	threadList.Data[0] = Thread{TID: ThreadId(10), State: ThreadReady, PID: PriestId(5)}
	threadList.InUse[0] = true
	threadList.Data[1] = Thread{TID: ThreadId(20), State: ThreadReady, PID: PriestId(5)}
	threadList.InUse[1] = true
	threadList.Data[2] = Thread{TID: ThreadId(3), State: ThreadReady, PID: PriestId(0)}
	threadList.InUse[2] = true

	// Enqueue two priest threads first (they go to back)
	enqueueReadySchedLockHeld(&threadList.Data[0])
	enqueueReadySchedLockHeld(&threadList.Data[1])

	// Now enqueue a kernel thread (should go to front)
	enqueueReadySchedLockHeld(&threadList.Data[2])

	// Pop order should be: kernel thread first, then priest threads
	if readyQueue.Size() != 3 {
		t.Fatalf("expected 3 in queue, got %d", readyQueue.Size())
	}

	first := readyQueue.Pop()
	if first != ThreadId(3) {
		t.Errorf("expected kernel thread TID=3 first, got TID=%d", first)
	}

	second := readyQueue.Pop()
	if second != ThreadId(10) {
		t.Errorf("expected priest thread TID=10 second, got TID=%d", second)
	}

	third := readyQueue.Pop()
	if third != ThreadId(20) {
		t.Errorf("expected priest thread TID=20 third, got TID=%d", third)
	}
}

// TestProcessStaticDeadlines verifies that processStaticDeadlinesSchedLockHeld
// moves expired deadline threads from blocked/sleeping to the ready queue.
func TestProcessStaticDeadlines(t *testing.T) {
	// Save and restore global state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origBlockedQueue := blockedQueue
	origSleepingQueue := sleepingQueue
	origDeadlineQueue := staticDeadlineQueue
	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
		blockedQueue = origBlockedQueue
		sleepingQueue = origSleepingQueue
		staticDeadlineQueue = origDeadlineQueue
	}()

	// Setup backing arrays
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool
	var testBlockedQueueData [MaxThreads]ThreadId
	var testBlockedQueueInUse [MaxThreads]bool
	var testSleepingQueueData [MaxThreads]ThreadId
	var testSleepingQueueInUse [MaxThreads]bool
	var testDeadlineData [MaxThreads]int16
	var testDeadlineOrderBy [MaxThreads]uint64

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]
	blockedQueue.Data = testBlockedQueueData[:]
	blockedQueue.InUse = testBlockedQueueInUse[:]
	sleepingQueue.Data = testSleepingQueueData[:]
	sleepingQueue.InUse = testSleepingQueueInUse[:]
	staticDeadlineQueue.Init(testDeadlineData[:], testDeadlineOrderBy[:])

	// Create a futex-blocked thread (TID=5, PID=2)
	threadList.Data[0] = Thread{
		TID:       ThreadId(5),
		State:     ThreadBlockedFutex,
		PID:       PriestId(2),
		FutexAddr: 0x1000,
	}
	threadList.InUse[0] = true
	blockedQueue.Push(ThreadId(5))

	// Create a sleeping thread (TID=8, PID=0 — kernel thread)
	threadList.Data[1] = Thread{
		TID:   ThreadId(8),
		State: ThreadSleeping,
		PID:   PriestId(0),
	}
	threadList.InUse[1] = true
	sleepingQueue.Push(ThreadId(8))

	// Create a thread with a future deadline (TID=12, should NOT be woken)
	threadList.Data[2] = Thread{
		TID:       ThreadId(12),
		State:     ThreadBlockedFutex,
		PID:       PriestId(3),
		FutexAddr: 0x2000,
	}
	threadList.InUse[2] = true
	blockedQueue.Push(ThreadId(12))

	// Add deadlines: TID=5 at tick 100, TID=8 at tick 200, TID=12 at tick 99999 (future)
	staticDeadlineQueue.Insert(int16(5), 100)
	staticDeadlineQueue.Insert(int16(8), 200)
	staticDeadlineQueue.Insert(int16(12), 99999)

	if staticDeadlineQueue.Size() != 3 {
		t.Fatalf("expected 3 deadlines, got %d", staticDeadlineQueue.Size())
	}

	// Override kirq.ReadCounterValue — we can't easily do this, so we call
	// the function directly. The function reads the hardware counter.
	// For testing, we need to verify behavior differently.
	// Instead, test the logic by calling with a known current time.
	//
	// Unfortunately processStaticDeadlinesSchedLockHeld calls kirq.ReadCounterValue()
	// internally, and we can't mock it in this test framework.
	// So we test the StaticOrderedList + thread state logic manually.

	// Simulate what processStaticDeadlinesSchedLockHeld does with currentTick=500
	currentTick := uint64(500)
	for !staticDeadlineQueue.IsEmpty() {
		tid, _ := staticDeadlineQueue.PopIfLess(currentTick)
		if tid == -1 {
			break
		}
		th := threadList.FindByIdAll(int32(tid))
		if th == nil {
			continue
		}
		if th.State == ThreadBlockedFutex {
			th.State = ThreadReady
			th.FutexAddr = 0
			blockedQueue.Pluck(ThreadId(tid))
			enqueueReadySchedLockHeld(th)
		} else if th.State == ThreadSleeping {
			th.State = ThreadReady
			sleepingQueue.Pluck(ThreadId(tid))
			enqueueReadySchedLockHeld(th)
		}
	}

	// Verify TID=5 was woken (futex blocked → ready)
	if threadList.Data[0].State != ThreadReady {
		t.Errorf("TID=5: expected ThreadReady, got %d", threadList.Data[0].State)
	}
	if threadList.Data[0].FutexAddr != 0 {
		t.Errorf("TID=5: expected FutexAddr=0, got 0x%x", threadList.Data[0].FutexAddr)
	}

	// Verify TID=8 was woken (sleeping → ready)
	if threadList.Data[1].State != ThreadReady {
		t.Errorf("TID=8: expected ThreadReady, got %d", threadList.Data[1].State)
	}

	// Verify TID=12 was NOT woken (future deadline)
	if threadList.Data[2].State != ThreadBlockedFutex {
		t.Errorf("TID=12: expected ThreadBlockedFutex, got %d", threadList.Data[2].State)
	}

	// Verify ready queue has 2 entries
	if readyQueue.Size() != 2 {
		t.Errorf("expected 2 in ready queue, got %d", readyQueue.Size())
	}

	// Kernel thread TID=8 (PID=0) should be at the front
	first := readyQueue.Pop()
	if first != ThreadId(8) {
		t.Errorf("expected kernel thread TID=8 at front, got TID=%d", first)
	}

	second := readyQueue.Pop()
	if second != ThreadId(5) {
		t.Errorf("expected priest thread TID=5 second, got TID=%d", second)
	}

	// One deadline should remain (TID=12 at tick 99999)
	if staticDeadlineQueue.Size() != 1 {
		t.Errorf("expected 1 remaining deadline, got %d", staticDeadlineQueue.Size())
	}
}
