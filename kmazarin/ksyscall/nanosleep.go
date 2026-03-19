package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

// SyscallNanosleep implements the nanosleep(2) syscall
// Sleeps for the requested duration using the deadline queue.
// If no deadline queue is available, falls back to yield behavior.
//
//go:nosplit
func SyscallNanosleep(req, rem, _, _, _, _ uint64) int64 {
	// req points to timespec struct: {tv_sec: i64, tv_nsec: i64}
	// rem would get remaining time if interrupted
	_ = rem // We don't support interruption

	if req == 0 {
		return 0 // No-op if no request
	}

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(req) {
		return -14 // EFAULT
	}

	// Read the timespec structure
	ts, ok := kmem.ReadUserInt64Pair(uintptr(req))
	if !ok {
		return -14 // EFAULT
	}
	seconds := ts[0]
	nanoseconds := ts[1]

	// Get current thread TID (not index!)
	// AddDeadline uses WakeThreadAction which calls FindById, so it needs the TID
	currentTID := int32(GetCurrentThreadTID())

	// Get timer frequency
	frequency := uint64(kirq.GetTimerFrequency())

	// Calculate ticks to sleep
	// ticks = (seconds * frequency) + (nanoseconds * frequency / 1e9)
	var ticks uint64
	if seconds > 0 {
		ticks = uint64(seconds) * frequency
	}
	if nanoseconds > 0 {
		ticks += (uint64(nanoseconds) * frequency) / 1000000000
	}

	// If requested sleep is 0, just yield
	if ticks == 0 {
		nextThread := ThreadFindReady()
		if nextThread != 0 {
			SetSyscallSwitchTarget(nextThread)
		}
		return 0
	}

	// Calculate deadline tick
	currentTick := kirq.ReadCounterValue()
	deadline := currentTick + ticks

	// Diagnostic: log first few nanosleep calls per shepherd
	pid := getCurrentThreadSID()
	if pid > 0 {
		n := atomic.AddUint64(&dbgNanosleepCount, 1)
		if n <= 5 {
			serial.RawUARTPuts("[ns:")
			serial.RawUARTDecimal(uint64(uint16(currentTID)))
			serial.RawUART(']')
		}
	}

	// Add to static deadline queue (always available, initialized in InitThreads)
	AddDeadlineStatic(deadline, currentTID)

	// Block current thread and context-switch to another.
	// ThreadBlockSleep unconditionally marks the thread sleeping. If no
	// ready thread exists, it enters idleWaitForReadyThread (WFI loop)
	// until the timer ISR processes our deadline and wakes a thread.
	nextThread := ThreadBlockSleep()
	SetSyscallSwitchTarget(nextThread)

	return 0
}

var dbgNanosleepCount uint64
