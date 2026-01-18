package kthread

// HandleTimerPreemption is called from the timer IRQ handler to check if the
// current KThread's time slice has expired and preemption is needed.
//
// This function should be called from the timer handler AFTER processing
// any Go runtime preemption logic, but BEFORE returning from the interrupt.
//
// TODO: Assembly integration point (kirq/timer_arm64.s or similar):
//   In the timer IRQ handler, after existing preemption logic:
//     BL    ·HandleTimerPreemption(SB)
//
// Returns true if a context switch is needed (caller should save context
// and call Schedule).
//
//go:nosplit
func HandleTimerPreemption() bool {
	kt := GetCurrentKThread()
	if kt == nil {
		return false // No KThread context
	}

	// Never preempt kernel threads
	if kt.IsKernelThread {
		return false
	}

	// Don't preempt blocked threads
	if kt.TimeSlice == SignalTicksLeft {
		return false
	}

	// Decrement priest affinity counter
	if kt.Priest != nil && kt.Priest.AffinityCounter > 0 {
		kt.Priest.AffinityCounter--
	}

	// Decrement thread time slice
	kt.TimeSlice--

	if kt.TimeSlice <= 0 {
		// Time slice exhausted - preemption needed
		return true
	}

	return false
}

// SaveContextAndSchedule is called when HandleTimerPreemption returns true.
// It saves the current context from the exception frame and schedules the
// next thread.
//
// framePtr: pointer to the exception frame containing saved registers
//
// TODO: This function needs to:
//   1. Save context from exception frame to kt.Context
//   2. Call Schedule() which will switch to another thread
//   3. The new thread's context is loaded by Schedule (never returns)
//
//go:nosplit
func SaveContextAndSchedule(framePtr uintptr) {
	kt := GetCurrentKThread()
	if kt == nil {
		return
	}

	// TODO: Save context from exception frame to kt.Context
	// This is similar to SaveContextFromFrame in thread.go but for KThread
	//
	// kt.Context.X[0] = frame.X0
	// kt.Context.X[1] = frame.X1
	// ... (all 31 registers)
	// kt.Context.SP = frame.SP_EL0
	// kt.Context.ELR = frame.ELR_EL1
	// kt.Context.SPSR = frame.SPSR_EL1

	_ = framePtr // TODO: implement context save

	// Mark as ready (no longer running)
	kt.State = KThreadReady

	// Schedule will pick next thread and restore its context
	Schedule()
	// Never returns - control transfers to new thread
}

// PreemptionTickPeriod returns the timer tick period in milliseconds.
// This is used to configure the timer to fire at regular intervals.
func PreemptionTickPeriod() int32 {
	return TickPeriodMs
}

// GetCurrentTimeSlice returns the current thread's remaining time slice.
// Returns 0 if no current thread.
func GetCurrentTimeSlice() int32 {
	kt := GetCurrentKThread()
	if kt == nil {
		return 0
	}
	return kt.TimeSlice
}

// SetThreadBlocked marks the current thread as blocked with special time slice.
// Called when a thread enters a blocking syscall (futex_wait, etc.)
func SetThreadBlocked() {
	kt := GetCurrentKThread()
	if kt == nil {
		return
	}
	kt.State = KThreadBlocked
	kt.TimeSlice = SignalTicksLeft
}
