package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"sync/atomic"
)

// Futex call statistics for debugging fairness
var (
	FutexWaitCalls    uint64 // Total FUTEX_WAIT calls
	FutexWaitBlocked  uint64 // Calls that blocked thread (switched to another)
	FutexWaitEagain   uint64 // Calls that returned EAGAIN (same thread continues)
	FutexWakeCalls    uint64 // Total FUTEX_WAKE calls
	KernelSVCCount    uint64 // SVCs from kernel threads (TID < 10)
	TotalSVCCount     uint64 // All SVCs from all threads
	DbgFutexExplicitTimeout uint64 // Deadlines with explicit timeout
	DbgStdioFutexAddr       uint64 // Last futex address for stdio debug
	DbgStdioFutexSyscallNum uint64 // Last syscall number from stdio's main
	priestSyscallCount      uint64 // Count of priest syscalls (for debug logging)
)

// PrintFutexStats prints futex statistics for debugging
func PrintFutexStats() (waitCalls, blocked, eagain, wakeCalls uint64) {
	return atomic.LoadUint64(&FutexWaitCalls),
		atomic.LoadUint64(&FutexWaitBlocked),
		atomic.LoadUint64(&FutexWaitEagain),
		atomic.LoadUint64(&FutexWakeCalls)
}

// TODO(ABI0): This file uses the tail-call stub workaround for SyscallFutex.
// We should investigate why the compiler-generated ABI0 wrapper doesn't work
// correctly when called via function pointer from the dispatch table, and fix
// our code to be compatible with it. See abi_stubs_arm64.s for details.

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// syscallFutexInternal implements the futex(2) syscall
// Properly blocks threads on FUTEX_WAIT and wakes them on FUTEX_WAKE
//
//go:nosplit
//go:noinline
func syscallFutexInternal(uaddr, op, val, timeout, uaddr2, val3 uint64) int64 {
	// Validate user address - reject NULL and kernel addresses
	if !isValidUserAddr(uaddr) {
		return -14 // EFAULT
	}

	// Mask off the private flag
	opMasked := int32(op) & 0x7F

	switch opMasked {
	case FutexWait:
		atomic.AddUint64(&FutexWaitCalls, 1)

		// FUTEX_WAIT: Block until value changes or we're woken
		// Check if value matches expected (spurious wakeup check)
		// Read through kernel linear map (safe for SUM/PAN).
		// Aligned 32-bit reads are atomic on all supported architectures.
		currentVal, ok := kmem.ReadUserUint32(uintptr(uaddr))
		if !ok {
			return -14 // EFAULT
		}

		if currentVal != uint32(val) {
			atomic.AddUint64(&FutexWaitEagain, 1)
			return -11 // -EAGAIN: value already changed
		}

		// Add a deadline only when an explicit timeout is provided.
		// Untimed futex_wait relies on futex_wake to wake the thread.
		if timeout != 0 {
			frequency := uint64(kirq.GetTimerFrequency())
			atomic.AddUint64(&DbgFutexExplicitTimeout, 1)
			ts, ok := kmem.ReadUserInt64Pair(uintptr(timeout))
			if !ok {
				return -14 // EFAULT
			}
			seconds := ts[0]
			nanoseconds := ts[1]
			var ticks uint64
			if seconds > 0 {
				ticks = uint64(seconds) * frequency
			}
			if nanoseconds > 0 {
				ticks += (uint64(nanoseconds) * frequency) / 1000000000
			}
			if ticks == 0 {
				ticks = 1
			}
			currentTick := kirq.ReadCounterValue()
			deadline := currentTick + ticks
			currentTID := int32(GetCurrentThreadTID())
			AddDeadlineStatic(deadline, currentTID)
		}

		// Try to find another thread to run and block current thread
		// ThreadBlockFutex re-checks the value under lock to prevent missed wakeup race.
		// It only marks us blocked if value still matches and there's a thread to switch to.
		nextThread := ThreadBlockFutex(uaddr, uint32(val))

		if nextThread != 0 {
			// Successfully blocked - context switch to next thread
			atomic.AddUint64(&FutexWaitBlocked, 1)
			SetSyscallSwitchTarget(nextThread)
			// Return 0 - when we're woken up later, we'll return success
			return 0
		}

		// Either no ready thread to switch to, OR the value changed (wake happened).
		// Return EAGAIN to let Go's runtime handle internal goroutine scheduling.
		// This is important for fairness: the thread continues and Go can
		// switch to another runnable goroutine within the same thread.
		atomic.AddUint64(&FutexWaitEagain, 1)
		return -11 // -EAGAIN

	case FutexWake:
		atomic.AddUint64(&FutexWakeCalls, 1)
		// FUTEX_WAKE: Wake up to 'val' threads waiting on this address
		woken := ThreadWakeFutex(uaddr, int32(val))
		return int64(woken)

	default:
		// Unknown futex operation
		return -38 // ENOSYS
	}
}
