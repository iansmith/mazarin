package kthread

// Schedule picks the next thread to run.
// Called when current thread blocks or its time slice expires.
func Schedule() {
	oldThread := cpuState[0].CurrentThread

	// Put old thread back in run queue if still runnable
	if oldThread != nil && oldThread.State == KThreadRunning {
		oldThread.State = KThreadReady
		runQueueLock.Lock()
		oldThread.schedNode = runQueue.PushBack(oldThread)
		runQueueLock.Unlock()
	}

	// Get current priest for affinity
	var currentPriest *PCB
	if oldThread != nil {
		currentPriest = oldThread.Priest
	}

	newThread := selectNextThread(currentPriest)

	if newThread == nil {
		// No ready threads - idle loop
		cpuState[0].CurrentThread = nil
		idleLoop()
		Schedule() // Retry after wakeup
		return
	}

	// Update affinity if switching priests
	if currentPriest != nil && newThread.Priest != currentPriest {
		newThread.Priest.AffinityCounter = PriestAffinityTicks
	}

	newThread.State = KThreadRunning
	newThread.CurrentCPU = 0
	newThread.TimeSlice = ThreadTimeSliceTicks
	cpuState[0].CurrentThread = newThread

	// Switch page tables if different priest
	if oldThread != nil && oldThread.Priest != newThread.Priest {
		switchPageTable(newThread.Priest.PageTableRoot)
	}

	// Restore context
	restoreContext(&newThread.Context)
}

// selectNextThread chooses the next thread from the run queue.
// Uses priest affinity to reduce TLB flushes.
func selectNextThread(currentPriest *PCB) *KThread {
	runQueueLock.Lock()
	defer runQueueLock.Unlock()

	if runQueue.IsEmpty() {
		return nil
	}

	// If affinity counter > 0, prefer same-priest threads
	if currentPriest != nil && currentPriest.AffinityCounter > 0 {
		for node := runQueue.Front(); node != nil; node = node.Next() {
			kt := node.Value
			if kt.Priest == currentPriest && !kt.IsKernelThread {
				runQueue.Remove(node)
				return kt
			}
		}
	}

	// If affinity exhausted, prefer OTHER priests to ensure fairness
	if currentPriest != nil && currentPriest.AffinityCounter <= 0 {
		for node := runQueue.Front(); node != nil; node = node.Next() {
			kt := node.Value
			if kt.Priest != currentPriest && !kt.IsKernelThread {
				runQueue.Remove(node)
				return kt
			}
		}
	}

	// Fallback: just take first available
	return runQueue.PopFront()
}

// idleLoop waits for threads to become runnable.
func idleLoop() {
	// Enable interrupts
	enableInterrupts()

	for {
		runQueueLock.Lock()
		hasThreads := !runQueue.IsEmpty()
		runQueueLock.Unlock()

		if hasThreads {
			return
		}

		wfi() // ARM64 Wait For Interrupt
	}
}

// RemoveFromRunQueue removes a thread from the run queue.
func RemoveFromRunQueue(kt *KThread) {
	runQueueLock.Lock()
	if kt.schedNode != nil {
		runQueue.Remove(kt.schedNode)
		kt.schedNode = nil
	}
	runQueueLock.Unlock()
}

// AddToRunQueue adds a thread to the run queue.
func AddToRunQueue(kt *KThread) {
	runQueueLock.Lock()
	kt.schedNode = runQueue.PushBack(kt)
	runQueueLock.Unlock()
}

// BlockCurrentThread marks current thread as blocked.
func BlockCurrentThread() {
	kt := GetCurrentKThread()
	if kt == nil {
		return
	}

	kt.State = KThreadBlocked
	kt.TimeSlice = SignalTicksLeft

	RemoveFromRunQueue(kt)
	Schedule()
}

// UnblockThread wakes a blocked thread.
func UnblockThread(kt *KThread) {
	kt.State = KThreadReady
	kt.TimeSlice = ThreadTimeSliceTicks
	AddToRunQueue(kt)
}

// Assembly function declarations - implemented in sched_arm64.s
func enableInterrupts()
func disableInterrupts() uint64
func restoreInterrupts(daif uint64)
func wfi()
func switchPageTable(root uint64)
func restoreContext(ctx *ThreadContext)
