//go:build arm64 && !test_no_asm

package main

import (
	"mazzy/kmazarin/ds"
	"sync/atomic"
	"testing"
	"unsafe"
)

// TestSimplePreemption tests the basic preemption logic with static data structures
func TestSimplePreemption(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origCurrentThread := atomic.LoadPointer(&CurrentThread)
	origTimerFreq := timerFrequencyHz
	origSpinBackoff := ds.SpinBackoffTicks

	defer func() {
		// Restore original state
		threadList = origThreadList
		readyQueue = origReadyQueue
		atomic.StorePointer(&CurrentThread, origCurrentThread)
		timerFrequencyHz = origTimerFreq
		ds.SpinBackoffTicks = origSpinBackoff
	}()

	// Initialize test environment
	timerFrequencyHz = 62500000 // 62.5MHz
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Setup backing arrays for test (minimal size)
	var testThreadListData [10]Thread
	var testThreadListInUse [10]bool
	var testReadyQueueData [10]ThreadId
	var testReadyQueueInUse [10]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Setup test timing values
	preemptThresholdTicks := uint64(6250000) // 100ms @ 62.5MHz
	threadStartTime := uint64(1000)
	currentTime := threadStartTime + preemptThresholdTicks + 1000 // Exceeded by 1000 ticks

	// Create Thread 14 (currently running, timeslice exhausted)
	threadList.Data[0] = Thread{
		TID:            ThreadId(14),
		State:          ThreadRunning,
		StartTick:      threadStartTime,
		PreemptElapsed: 0,
		LastSeenG:      0x1000,
		MPtr:           0x2000,
		GPtr:           0x3000,
	}
	threadList.InUse[0] = true
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&threadList.Data[0]))

	// Create Thread 32 (on ready queue)
	threadList.Data[1] = Thread{
		TID:            ThreadId(32),
		State:          ThreadReady,
		StartTick:      0,
		PreemptElapsed: 5000,
		LastSeenG:      0x4000,
		MPtr:           0x5000,
		GPtr:           0x6000,
	}
	threadList.InUse[1] = true

	// Add Thread 32 to ready queue
	readyQueue.Push(ThreadId(32))

	// Create test helpers
	daifPair := &DAIFPair{}
	testClock := NewTestClock([]uint64{
		currentTime, // First call returns current time
	})

	testFunc := SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck: func(checkpoint string) {
			t.Logf("StateCheck: %s", checkpoint)
		},
	}

	t.Logf("Before preemption check:")
	t.Logf("  Thread 14: state=%d, running=%v, StartTick=%d",
		threadList.Data[0].State, threadList.Data[0].State == ThreadRunning, threadList.Data[0].StartTick)
	t.Logf("  Thread 32: state=%d, ready=%v",
		threadList.Data[1].State, threadList.Data[1].State == ThreadReady)
	t.Logf("  Current thread: TID=%d", (*Thread)(atomic.LoadPointer(&CurrentThread)).TID)
	t.Logf("  Ready queue size: %d", readyQueue.Size())
	t.Logf("  Current time: %d", currentTime)
	t.Logf("  Elapsed ticks: %d (threshold=%d)", currentTime-threadStartTime, preemptThresholdTicks)

	// Call the preemption check directly
	contextPtr := checkThreadPreemptionImpl(&testFunc, 0)

	t.Logf("After preemption check:")
	t.Logf("  Context switch returned: 0x%x (non-zero = switch occurred)", contextPtr)
	t.Logf("  Thread 14: state=%d, ready=%v", threadList.Data[0].State, threadList.Data[0].State == ThreadReady)
	t.Logf("  Thread 32: state=%d, running=%v", threadList.Data[1].State, threadList.Data[1].State == ThreadRunning)
	CurrentThreadPtr := (*Thread)(atomic.LoadPointer(&CurrentThread))
	t.Logf("  Current thread: TID=%d", CurrentThreadPtr.TID)
	t.Logf("  Ready queue size: %d", readyQueue.Size())

	// Verify preemption occurred
	if contextPtr == 0 {
		t.Error("Expected context switch (non-zero context pointer), got 0")
	}

	// Verify Thread 32 is now running
	if threadList.Data[1].State != ThreadRunning {
		t.Errorf("Thread 32 should be running, got state=%d", threadList.Data[1].State)
	}
	if CurrentThreadPtr.TID != ThreadId(32) {
		t.Errorf("Current thread should be 32, got %d", CurrentThreadPtr.TID)
	}

	// Verify Thread 14 is now ready (should be on ready queue)
	if threadList.Data[0].State != ThreadReady {
		t.Errorf("Thread 14 should be ready, got state=%d", threadList.Data[0].State)
	}

	// Find Thread 14 in ready queue
	found := false
	for i := 0; i < len(readyQueue.Data); i++ {
		if readyQueue.InUse[i] && readyQueue.Data[i] == ThreadId(14) {
			found = true
			t.Logf("  Found Thread 14 at ready queue index %d", i)
			break
		}
	}
	if !found {
		t.Error("Thread 14 should be on ready queue after preemption")
	}

	// Verify lock discipline - DAIF calls must be balanced
	if !daifPair.Balanced() {
		t.Errorf("Unbalanced DAIF calls: counter=%d (should be 0)", daifPair.daifCalls)
	} else {
		t.Logf("Lock discipline: DAIF calls balanced ✓")
	}
}
