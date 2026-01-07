//go:build qemuvirt && aarch64

package main

// ============================================================================
// SYSCALL HANDLERS - Called from assembly
// ============================================================================
//
// These functions implement the syscall logic. Assembly calls these and then
// performs context switches based on the return values.
//
// Return convention:
//   x0 = syscall return value (or action code)
//   x1 = target thread index (if context switch needed)

// SyscallCloneHandler handles the clone syscall
// Called from assembly with clone parameters already extracted from stack
// Returns: (tid int64, switchTo int32)
//   - tid: TID of new thread (returned to parent)
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallCloneHandler(stack, entryFunc, mPtr, gPtr uint64) (tid int64, switchTo int32) {
	// Create new thread in READY state
	newTID := ThreadCreate(stack, entryFunc, mPtr, gPtr)
	if newTID < 0 {
		return -1, -1 // Error, return -1 to caller
	}

	// Return TID to parent, don't switch yet
	// The new thread will run when parent blocks
	return int64(newTID), -1
}

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// SyscallFutexHandler handles the futex syscall
// Returns: (result int64, switchTo int32)
//   - result: 0 for WAIT, number woken for WAKE
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallFutexHandler(uaddr uint64, op int32, val uint32) (result int64, switchTo int32) {
	// Mask off private flag
	opMasked := op & 0x7F

	switch opMasked {
	case FutexWait:
		// FUTEX_WAIT: Block until someone wakes us
		// Find next thread to run
		nextThread := ThreadBlockFutex(uaddr)
		if nextThread >= 0 {
			// Context switch to next thread
			return 0, nextThread
		}
		// No other thread ready - this is a problem!
		// For now, return immediately (spurious wakeup)
		threads[currentThreadIdx].State = ThreadRunning // Unblock ourselves
		return 0, -1

	case FutexWake:
		// FUTEX_WAKE: Wake up to 'val' threads blocked on this address
		woken := ThreadWakeFutex(uaddr, int32(val))
		return int64(woken), -1

	default:
		return 0, -1
	}
}

// SyscallNanosleepHandler handles the nanosleep syscall
// duration is in nanoseconds
// Returns: (result int64, switchTo int32)
//
//go:nosplit
//go:noinline
func SyscallNanosleepHandler(seconds uint64, nanoseconds uint64) (result int64, switchTo int32) {
	// Calculate total nanoseconds
	totalNs := seconds*1000000000 + nanoseconds

	// Convert to timer ticks
	// ticks = ns * freq / 1e9
	// To avoid overflow: ticks = (ns / 1000) * (freq / 1000000)
	//                         = (ns / 1000) * (freq_mhz)
	freqMHz := timerFrequencyHz / 1000000 // 62 for 62.5 MHz
	ticks := (totalNs / 1000) * freqMHz

	if ticks == 0 {
		ticks = 1 // Minimum 1 tick
	}

	// Block current thread
	nextThread := ThreadBlockSleep(ticks)
	if nextThread >= 0 {
		return 0, nextThread
	}

	// No other thread ready - busy wait
	// This shouldn't happen in practice, but handle it
	threads[currentThreadIdx].State = ThreadRunning
	return 0, -1
}

// TimerTickHandler is called from timer interrupt
// Checks for threads to wake and optionally does thread preemption
// Returns: switchTo int32 (-1 for no switch, >=0 to switch)
//
//go:nosplit
//go:noinline
func TimerTickHandler() int32 {
	// Increment global tick counter
	ThreadIncrementTick()

	// Check sleeping threads
	ThreadCheckSleepers()

	// TODO: Add round-robin thread preemption here
	// For now, only wake sleepers, don't force thread switch
	// This allows Go's scheduler to control thread execution

	return -1
}
