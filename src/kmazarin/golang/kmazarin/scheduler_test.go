//go:build arm64

package main

import (
	"kmazarin/ds"
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

// TestPriestPreference_DifferentPriest verifies that threadFindReadyPreferDifferentPriest
// selects a thread from a different priest when available.
//
// Scenario:
//   - Thread A (PID=1) is currently running
//   - Thread B (PID=1) is ready (same priest as A)
//   - Thread C (PID=2) is ready (different priest from A)
//   - Ready queue order: [B, C]
//
// Expected Result:
//   - threadFindReadyPreferDifferentPriest(1) returns Thread C (different priest preferred)
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

	// Call threadFindReadyPreferDifferentPriest with currentPID=1
	// Should select Thread C (PID=2) even though B is first
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

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

// TestPriestPreference_FallbackSamePriest verifies that threadFindReadyPreferDifferentPriest
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
//   - threadFindReadyPreferDifferentPriest(1) returns Thread B (FIFO fallback)
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

	// Call threadFindReadyPreferDifferentPriest with currentPID=1
	// Should fall back to FIFO (Thread B) since all are same priest
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

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
//   - threadFindReadyPreferDifferentPriest(0) returns Thread C (different priest preferred)
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

	// Call threadFindReadyPreferDifferentPriest with currentPID=0 (kernel)
	// Should select Thread C (PID=1) even though B is first
	selected := threadFindReadyPreferDifferentPriest(PriestId(0))

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

// TestPriestPreference_EmptyQueue verifies that threadFindReadyPreferDifferentPriest
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

	// Call threadFindReadyPreferDifferentPriest
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

	// Verify nil was returned
	if selected != nil {
		t.Errorf("Expected nil from empty queue, got TID=%d", selected.TID)
	}

	t.Logf("Correctly returned nil")
}

// TestPriestPreference_StaleEntries verifies that threadFindReadyPreferDifferentPriest
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

	// Call threadFindReadyPreferDifferentPriest with currentPID=1
	// Should skip stale Thread A and select Thread B
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

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

// TestPriestPreference_MultipleChoices verifies that threadFindReadyPreferDifferentPriest
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

	// Call threadFindReadyPreferDifferentPriest with currentPID=1
	// Should select Thread C (first different-priest in scan order)
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

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
