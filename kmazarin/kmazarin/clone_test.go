//go:build arm64 && !test_no_asm

package main

import (
	"mazzy/kmazarin/ds"
	"sync/atomic"
	"testing"
	"unsafe"
)

// TestCloneImmediateSwitch verifies that clone() switches to the new thread immediately
// rather than letting the parent continue running.
func TestCloneImmediateSwitch(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origCurrentThread := atomic.LoadPointer(&CurrentThread)
	origTimerFreq := timerFrequencyHz
	origSpinBackoff := ds.SpinBackoffTicks
	origSwitchTarget := syscallSwitchTarget

	defer func() {
		// Restore original state
		threadList = origThreadList
		readyQueue = origReadyQueue
		atomic.StorePointer(&CurrentThread, origCurrentThread)
		timerFrequencyHz = origTimerFreq
		ds.SpinBackoffTicks = origSpinBackoff
		syscallSwitchTarget = origSwitchTarget
	}()

	// Initialize test environment
	timerFrequencyHz = 62500000
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Setup backing arrays for test
	var testThreadListData [10]Thread
	var testThreadListInUse [10]bool
	var testReadyQueueData [10]ThreadId
	var testReadyQueueInUse [10]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create parent thread A (currently running)
	threadList.Data[0] = Thread{
		TID:   ThreadId(100),
		State: ThreadRunning,
	}
	threadList.InUse[0] = true
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&threadList.Data[0]))

	// Create test helpers
	daifPair := &DAIFPair{}
	testClock := NewTestClock([]uint64{1000, 1001, 1002})

	testFunc := SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck: func(checkpoint string) {
			t.Logf("StateCheck: %s", checkpoint)
		},
	}

	// Parameters for clone
	childStack := uint64(0x1000000)    // Child's stack pointer
	returnAddr := uint64(0x2000000)    // Return address (should NOT be used)
	spsr := uint64(0x0)                // Processor state
	mp := uint64(0x3000000)            // m pointer
	gp := uint64(0x4000000)            // g pointer
	fn := uint64(0x5000000)            // Entry function (should BE used)

	// Clear switch target before clone
	syscallSwitchTarget = 0

	t.Log("Calling CloneThread...")

	// Call clone
	tid := CloneThread(&testFunc, childStack, returnAddr, spsr, mp, gp, fn)

	t.Logf("CloneThread returned TID=%d", tid)

	// Verify TID is valid
	if tid < 0 {
		t.Fatalf("CloneThread returned error: %d", tid)
	}

	// Find the new thread (B)
	var childThread *Thread
	var childIdx int = -1
	for i := 0; i < len(threadList.Data); i++ {
		if threadList.InUse[i] && threadList.Data[i].TID == ThreadId(tid) {
			childThread = &threadList.Data[i]
			childIdx = i
			break
		}
	}

	if childThread == nil {
		t.Fatal("Could not find child thread in threadList")
	}

	t.Logf("Child thread found at index %d", childIdx)

	// TEST 1: Child state should be Running (not Ready)
	if childThread.State != ThreadRunning {
		t.Errorf("Child state should be ThreadRunning (1), got %d", childThread.State)
	} else {
		t.Log("PASS: Child state is ThreadRunning")
	}

	// TEST 2: Child should NOT be in ready queue
	childInQueue := false
	for i := 0; i < len(readyQueue.Data); i++ {
		if readyQueue.InUse[i] && readyQueue.Data[i] == ThreadId(tid) {
			childInQueue = true
			break
		}
	}
	if childInQueue {
		t.Error("Child should NOT be in ready queue (it runs immediately)")
	} else {
		t.Log("PASS: Child is not in ready queue")
	}

	// TEST 3: Child's ELR should be fn (entry function), NOT returnAddr
	if childThread.Context.ELR != fn {
		t.Errorf("Child Context.ELR should be fn (0x%x), got 0x%x", fn, childThread.Context.ELR)
		if childThread.Context.ELR == returnAddr {
			t.Error("  ERROR: ELR is set to returnAddr - this is the OLD broken behavior!")
		}
	} else {
		t.Log("PASS: Child Context.ELR is set to entry function (fn)")
	}

	// TEST 4: Child's SP should be the provided stack
	if childThread.Context.SP != childStack {
		t.Errorf("Child Context.SP should be 0x%x, got 0x%x", childStack, childThread.Context.SP)
	} else {
		t.Log("PASS: Child Context.SP is correct")
	}

	// TEST 5: SetSyscallSwitchTarget should have been called with child's context
	if syscallSwitchTarget == 0 {
		t.Error("SetSyscallSwitchTarget was NOT called - parent would continue running!")
	} else {
		expectedTarget := uintptr(unsafe.Pointer(&childThread.Context))
		if syscallSwitchTarget != expectedTarget {
			t.Errorf("SetSyscallSwitchTarget called with wrong target: got 0x%x, expected 0x%x",
				syscallSwitchTarget, expectedTarget)
		} else {
			t.Log("PASS: SetSyscallSwitchTarget called with child's context")
		}
	}

	// TEST 6: Parent (A) should still be current thread at this point
	// (DoContextSwitch hasn't run yet - that happens in assembly after syscall returns)
	currThread := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if currThread != &threadList.Data[0] {
		t.Errorf("CurrentThread should still be parent (index 0)")
	} else {
		t.Log("PASS: CurrentThread is still parent (A) - DoContextSwitch runs later in assembly")
	}

	// TEST 7: Child's X[28] should be gp (g register)
	if childThread.Context.X[28] != gp {
		t.Errorf("Child Context.X[28] should be gp (0x%x), got 0x%x", gp, childThread.Context.X[28])
	} else {
		t.Log("PASS: Child Context.X[28] (g register) is correct")
	}

	// Verify DAIF balance
	if !daifPair.Balanced() {
		t.Errorf("DAIF unbalanced: %d", daifPair.daifCalls)
	} else {
		t.Log("PASS: DAIF calls balanced")
	}

	t.Log("")
	t.Log("Summary: Clone correctly sets up for immediate switch to child thread")
}

// TestCloneParentEventuallyRuns verifies that after DoContextSwitch,
// the parent thread is properly saved and queued for later execution.
func TestCloneParentEventuallyRuns(t *testing.T) {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	origCurrentThread := atomic.LoadPointer(&CurrentThread)
	origTimerFreq := timerFrequencyHz
	origSpinBackoff := ds.SpinBackoffTicks
	origSwitchTarget := syscallSwitchTarget

	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
		atomic.StorePointer(&CurrentThread, origCurrentThread)
		timerFrequencyHz = origTimerFreq
		ds.SpinBackoffTicks = origSpinBackoff
		syscallSwitchTarget = origSwitchTarget
	}()

	// Initialize
	timerFrequencyHz = 62500000
	ds.InitSpinlockTiming(timerFrequencyHz)

	var testThreadListData [10]Thread
	var testThreadListInUse [10]bool
	var testReadyQueueData [10]ThreadId
	var testReadyQueueInUse [10]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]

	// Create parent thread A
	parentTID := ThreadId(100)
	threadList.Data[0] = Thread{
		TID:   parentTID,
		State: ThreadRunning,
	}
	threadList.InUse[0] = true
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(&threadList.Data[0]))

	daifPair := &DAIFPair{}
	testClock := NewTestClock([]uint64{1000, 1001, 1002, 1003, 1004})

	testFunc := SchedulerFunc{
		CurrentTime:          testClock.CurrentTime,
		DisableAndSaveDAIF:   daifPair.DisableAndSaveDAIF,
		EnableAndRestoreDAIF: daifPair.EnableAndRestoreDAIF,
		StateCheck:           func(checkpoint string) { t.Logf("StateCheck: %s", checkpoint) },
	}

	// Clone parameters
	childStack := uint64(0x1000000)
	returnAddr := uint64(0x2000000)
	spsr := uint64(0x0)
	mp := uint64(0x3000000)
	gp := uint64(0x4000000)
	fn := uint64(0x5000000)

	// Call clone
	childTID := CloneThread(&testFunc, childStack, returnAddr, spsr, mp, gp, fn)
	t.Logf("CloneThread returned child TID=%d", childTID)

	// Find child
	var childThread *Thread
	childIdx := int32(-1)
	for i := 0; i < len(threadList.Data); i++ {
		if threadList.InUse[i] && threadList.Data[i].TID == ThreadId(childTID) {
			childThread = &threadList.Data[i]
			childIdx = int32(i)
			break
		}
	}

	if childThread == nil {
		t.Fatal("Could not find child thread")
	}

	// Now simulate what assembly does after syscall returns:
	// Call doContextSwitchImpl with the switch target

	t.Log("Simulating DoContextSwitch (what assembly does after syscall)...")

	// DoContextSwitch expects framePtr - we pass 0 since SaveContextFromFrame handles nil
	// and the target context pointer
	ctx := doContextSwitchImpl(&testFunc, 0, childIdx)

	if ctx == nil {
		t.Fatal("doContextSwitchImpl returned nil")
	}

	// TEST 1: CurrentThread should now be the child
	currentPtr := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if currentPtr.TID != ThreadId(childTID) {
		t.Errorf("After DoContextSwitch, CurrentThread should be child (TID=%d), got TID=%d",
			childTID, currentPtr.TID)
	} else {
		t.Log("PASS: CurrentThread is now the child")
	}

	// TEST 2: Current thread index should be child's index
	currentIdx := threadToIdx(currentPtr)
	if currentIdx != childIdx {
		t.Errorf("Current thread index should be %d, got %d", childIdx, currentIdx)
	} else {
		t.Log("PASS: Current thread index is correct")
	}

	// TEST 3: Parent should now be in ready queue
	parentInQueue := false
	for i := 0; i < len(readyQueue.Data); i++ {
		if readyQueue.InUse[i] && readyQueue.Data[i] == parentTID {
			parentInQueue = true
			break
		}
	}
	if !parentInQueue {
		t.Error("Parent should be in ready queue after DoContextSwitch")
	} else {
		t.Log("PASS: Parent is in ready queue")
	}

	// TEST 4: Parent's state should be Ready
	if threadList.Data[0].State != ThreadReady {
		t.Errorf("Parent state should be ThreadReady (2), got %d", threadList.Data[0].State)
	} else {
		t.Log("PASS: Parent state is ThreadReady")
	}

	// TEST 5: Child's state should be Running
	if childThread.State != ThreadRunning {
		t.Errorf("Child state should be ThreadRunning (1), got %d", childThread.State)
	} else {
		t.Log("PASS: Child state is ThreadRunning")
	}

	// TEST 6: Returned context should be child's context
	if ctx != &childThread.Context {
		t.Error("DoContextSwitch should return pointer to child's context")
	} else {
		t.Log("PASS: DoContextSwitch returned child's context")
	}

	if !daifPair.Balanced() {
		t.Errorf("DAIF unbalanced: %d", daifPair.daifCalls)
	} else {
		t.Log("PASS: DAIF calls balanced")
	}

	t.Log("")
	t.Log("Summary: After DoContextSwitch, child runs and parent is queued")
}
