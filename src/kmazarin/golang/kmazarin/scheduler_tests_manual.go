package main

import "fmt"

// ManualSchedulerTest runs scheduler tests without testing.T framework
// This can be called directly from main() to validate scheduler behavior in QEMU
func ManualSchedulerTest() {
	fmt.Println("\n[SchedulerTest] === Running Manual Scheduler Tests ===")

	passed := 0
	failed := 0

	// Test 1: Different Priest Preference
	fmt.Println("\n[Test 1] threadFindReadyPreferDifferentPriest - Different Priest Available")
	if testPriestPreferenceDifferent() {
		fmt.Println("  ✓ PASS")
		passed++
	} else {
		fmt.Println("  ✗ FAIL")
		failed++
	}

	// Test 2: Same Priest Fallback
	fmt.Println("\n[Test 2] threadFindReadyPreferDifferentPriest - Same Priest Fallback")
	if testPriestPreferenceFallback() {
		fmt.Println("  ✓ PASS")
		passed++
	} else {
		fmt.Println("  ✗ FAIL")
		failed++
	}

	// Test 3: Empty Queue
	fmt.Println("\n[Test 3] threadFindReadyPreferDifferentPriest - Empty Queue")
	if testPriestPreferenceEmpty() {
		fmt.Println("  ✓ PASS")
		passed++
	} else {
		fmt.Println("  ✗ FAIL")
		failed++
	}

	// Summary
	fmt.Printf("\n[SchedulerTest] === Results: %d passed, %d failed ===\n", passed, failed)
	if failed == 0 {
		fmt.Println("[SchedulerTest] All tests passed!")
	}
}

// testPriestPreferenceDifferent tests that different priest is preferred
func testPriestPreferenceDifferent() bool {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup test data
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]
	readyQueue.Clear() // Reset queue state

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

	// Call with currentPID=1, should select Thread C (PID=2)
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

	// Verify
	if selected == nil {
		fmt.Println("    Error: Expected thread, got nil")
		return false
	}
	if selected.TID != ThreadId(20) {
		fmt.Printf("    Error: Expected TID=20, got TID=%d\n", selected.TID)
		return false
	}
	if selected.PID != PriestId(2) {
		fmt.Printf("    Error: Expected PID=2, got PID=%d\n", selected.PID)
		return false
	}

	return true
}

// testPriestPreferenceFallback tests fallback to same priest
func testPriestPreferenceFallback() bool {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup test data
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]
	readyQueue.Clear() // Reset queue state

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

	// Add both to ready queue
	readyQueue.Push(ThreadId(10))
	readyQueue.Push(ThreadId(20))

	// Call with currentPID=1, should fall back to FIFO (Thread B)
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

	// Verify
	if selected == nil {
		fmt.Println("    Error: Expected thread, got nil")
		return false
	}
	if selected.TID != ThreadId(10) {
		fmt.Printf("    Error: Expected TID=10 (FIFO), got TID=%d\n", selected.TID)
		return false
	}

	return true
}

// testPriestPreferenceEmpty tests empty queue handling
func testPriestPreferenceEmpty() bool {
	// Save original state
	origThreadList := threadList
	origReadyQueue := readyQueue
	defer func() {
		threadList = origThreadList
		readyQueue = origReadyQueue
	}()

	// Setup test data
	var testThreadListData [MaxThreads]Thread
	var testThreadListInUse [MaxThreads]bool
	var testReadyQueueData [MaxThreads]ThreadId
	var testReadyQueueInUse [MaxThreads]bool

	threadList.Data = testThreadListData[:]
	threadList.InUse = testThreadListInUse[:]
	readyQueue.Data = testReadyQueueData[:]
	readyQueue.InUse = testReadyQueueInUse[:]
	readyQueue.Clear() // Reset queue state

	// Don't add any threads (empty queue)

	// Call should return nil
	selected := threadFindReadyPreferDifferentPriest(PriestId(1))

	// Verify
	if selected != nil {
		fmt.Printf("    Error: Expected nil, got TID=%d\n", selected.TID)
		return false
	}

	return true
}
