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
	DbgLinuxFutexAddr       uint64 // Last futex address for linux shepherd debug
	DbgLinuxFutexSyscallNum uint64 // Last syscall number from linux's main
	shepherdSyscallCount      uint64 // Count of shepherd syscalls (for debug logging)
	GCCountBySID            [16]uint64 // Per-shepherd GC cycle counter (indexed by PID)
	NanosleepCallCount     uint64 // Total nanosleep calls (all threads)
	NanosleepZeroTickCount uint64 // Nanosleep calls with ticks==0 (instant yield)
	NanosleepRealSleepCount uint64 // Nanosleep calls that actually block
	NanosleepDispatchedSID0 uint64 // Nanosleep dispatched for SID 0
	NanosleepEarlyNull      uint64
	NanosleepEarlyEfault    uint64
	NanosleepEarlyReadFail  uint64
	YieldCallCount     uint64     // Total sched_yield calls (all threads)
	YieldSwitchCount   uint64     // Yields that actually found a thread to switch to
	YieldNoReadyCount  uint64     // Yields that found no ready thread
	SVCCountBySID      [32]uint64 // Per-SID SVC counter for diagnostics
	// SID0 per-syscall-number counters for diagnosing kernel thread load.
	SID0SyscallCounts [256]uint64 // indexed by syscall number (clamped to 255)
	// DbgTraceSID, when >= 0, causes DispatchSyscall to log syscall numbers
	// for the matching SID. Set from ext2 timing hooks to trace fs during stall.
	DbgTraceSID int32 = -1
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
	// Reject NULL but allow kernel addresses. The Go runtime running in
	// kmazarin calls futex with kernel addresses for its own mutexes.
	// isValidUserAddr would reject these when CurrentShepherd() is non-nil
	// (Go scheduler can run kernel goroutines on a shepherd M). Futex is
	// safe: WAIT only reads, WAKE uses the address as a lookup key.
	if uaddr == 0 {
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

		// Add a deadline only if the caller provided an explicit timeout.
		// Untimed futex_wait blocks until futex_wake — no implicit deadline.
		if timeout != 0 {
			atomic.AddUint64(&DbgFutexExplicitTimeout, 1)
			ts, ok := kmem.ReadUserInt64Pair(uintptr(timeout))
			if !ok {
				return -14 // EFAULT
			}
			frequency := uint64(kirq.GetTimerFrequency())
			var ticks uint64
			seconds := ts[0]
			nanoseconds := ts[1]
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

		// Block current thread and context-switch to another.
		// ThreadBlockFutex re-checks the value under lock to prevent missed
		// wakeup race. If the value still matches, the thread is unconditionally
		// marked ThreadBlockedFutex. If no thread is ready, ThreadBlockFutex
		// enters idleWaitForReadyThread (WFI loop) until the timer wakes one.
		// Returns 0 only if the value changed (EAGAIN — caller retries).
		nextThread := ThreadBlockFutex(uaddr, uint32(val))

		if nextThread != 0 {
			// Successfully blocked - context switch to next thread
			atomic.AddUint64(&FutexWaitBlocked, 1)
			SetSyscallSwitchTarget(nextThread)
			return 0
		}

		// Value changed before we could block — retry the CAS.
		atomic.AddUint64(&FutexWaitEagain, 1)
		return -11 // -EAGAIN

	case FutexWake:
		atomic.AddUint64(&FutexWakeCalls, 1)
		// FUTEX_WAKE: Wake up to 'val' threads waiting on this address.
		// Use ThreadWakeFutexWithSwitch to get the first woken thread's
		// context pointer — then immediately context-switch to it via
		// SetSyscallSwitchTarget. This matches Linux's behavior where
		// futex_wake → wake_up_process → try_to_wake_up preempts the
		// caller in favor of the woken thread.
		woken, ctxPtr := ThreadWakeFutexWithSwitch(uaddr, int32(val))
		if ctxPtr != 0 {
			SetSyscallSwitchTarget(ctxPtr)
		}
		return int64(woken)

	default:
		// Unknown futex operation
		return -38 // ENOSYS
	}
}
