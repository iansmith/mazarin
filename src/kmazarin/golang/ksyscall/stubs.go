
package ksyscall

import (
	"kmazarin/kirq"
	"unsafe"
)

// ============================================================================
// Simple stub syscalls that return constants or success
// ============================================================================

// SyscallGetpid returns the process ID
// Always return 1 (init process)
//
//go:nosplit
func SyscallGetpid(_, _, _, _, _, _ uint64) int64 {
	return 1
}

// SyscallGettid returns the thread ID
// Always return 1 (main thread)
//
//go:nosplit
func SyscallGettid(_, _, _, _, _, _ uint64) int64 {
	return 1
}

// SyscallSchedYield yields the processor to another ready thread
// If no other threads are ready, returns immediately
//
//go:nosplit
func SyscallSchedYield(_, _, _, _, _, _ uint64) int64 {
	// Try to find another ready thread to switch to
	nextThread := threadFindReadyForYield()

	if nextThread != 0 {
		// Found a ready thread - yield to it
		setSyscallSwitchTargetForYield(nextThread)
	}
	// else: No other ready thread, return immediately

	return 0 // Success
}

// SyscallSigaltstack sets up an alternate signal stack
// We don't support signals, return success
//
//go:nosplit
func SyscallSigaltstack(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallSchedSetaffinity sets CPU affinity
// We only have one CPU, return success
//
//go:nosplit
func SyscallSchedSetaffinity(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallPrctl performs process control operations
// Return success for PR_SET_VMA (0x53564D41) and other operations
// to avoid confusing the Go runtime
//
//go:nosplit
func SyscallPrctl(option, _, _, _, _, _ uint64) int64 {
	// PR_SET_VMA = 0x53564D41 is used by Go runtime to name memory regions
	// We don't implement it but should return success
	return 0 // Success (stub)
}

// SyscallMprotect changes memory protection
// We don't enforce protection, return success
//
//go:nosplit
func SyscallMprotect(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallPrlimit64 gets/sets resource limits
// We don't enforce limits, return success
//
//go:nosplit
func SyscallPrlimit64(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallMadvise gives advice about memory usage
// We ignore the advice, return success
//
//go:nosplit
func SyscallMadvise(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallBrk changes the program break
// We don't support brk, return current value (no change)
//
//go:nosplit
func SyscallBrk(addr, _, _, _, _, _ uint64) int64 {
	// Return the requested address unchanged (no-op)
	return int64(addr)
}

// ============================================================================
// I/O related stubs
// ============================================================================

// SyscallIoSetup sets up async I/O context
// We don't support async I/O, return -ENOSYS
//
//go:nosplit
func SyscallIoSetup(_, _, _, _, _, _ uint64) int64 {
	return -38 // -ENOSYS
}

// SyscallEventfd creates an event file descriptor
// Return fake eventfd (11)
//
//go:nosplit
func SyscallEventfd(_, _, _, _, _, _ uint64) int64 {
	return 11 // Fake eventfd
}

// SyscallEpollCreate creates an epoll file descriptor
// Return fake epoll fd (10)
//
//go:nosplit
func SyscallEpollCreate(_, _, _, _, _, _ uint64) int64 {
	return 10 // Fake epoll fd
}

// SyscallEpollCtl controls an epoll file descriptor
// Return success
//
//go:nosplit
func SyscallEpollCtl(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallEpollPwait waits for events on an epoll fd
// Return 0 (no events)
//
//go:nosplit
func SyscallEpollPwait(_, _, _, _, _, _ uint64) int64 {
	return 0 // No events
}

// SyscallFcntl performs file control operations
// Go runtime calls fcntl(fd, F_GETFD) to verify stdin/stdout/stderr
//
//go:nosplit
func SyscallFcntl(fd, cmd, _, _, _, _ uint64) int64 {
	// F_GETFD = 1: Get file descriptor flags
	if cmd == 1 && fd <= 2 {
		return 0 // No flags set, fd is valid
	}
	return -38 // -ENOSYS
}

// SyscallClose closes a file descriptor
// Return success for now
//
//go:nosplit
func SyscallClose(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallRead reads from a file descriptor
// Return 0 (EOF) for now
//
//go:nosplit
func SyscallRead(_, _, _, _, _, _ uint64) int64 {
	return 0 // EOF
}

// SyscallOpenat opens a file
// Return fake fd (5) for now
//
//go:nosplit
func SyscallOpenat(_, _, _, _, _, _ uint64) int64 {
	return 5 // Fake fd
}

// ============================================================================
// Signal stubs
// ============================================================================

// SyscallRtSigaction sets up a signal action
// We don't support signals, return success
//
//go:nosplit
func SyscallRtSigaction(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallKill sends a signal
// We don't support signals, return success
//
//go:nosplit
func SyscallKill(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallTkill sends a signal to a thread
// We don't support signals, return success
//
//go:nosplit
func SyscallTkill(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// SyscallTgkill sends a signal to a thread in a process
// We don't support signals, return success
//
//go:nosplit
func SyscallTgkill(_, _, _, _, _, _ uint64) int64 {
	return 0 // Success
}

// ============================================================================
// Time stubs
// ============================================================================

// SyscallClockGettime gets the current time
// arg0: clockid (ignored - we only support one clock)
// arg1: pointer to timespec structure (tv_sec, tv_nsec)
//
//go:nosplit
func SyscallClockGettime(clockid, timespecPtr, _, _, _, _ uint64) int64 {
	_ = clockid // Ignore clock ID for now

	if timespecPtr == 0 {
		return -14 // EFAULT - invalid pointer
	}

	// Read current timer counter value
	counterValue := kirq.ReadCounterValue()

	// Get timer frequency (Hz)
	frequency := uint64(kirq.GetTimerFrequency())

	// Convert to seconds and nanoseconds
	// seconds = counterValue / frequency
	// nanoseconds = (counterValue % frequency) * 1000000000 / frequency
	seconds := counterValue / frequency
	remainder := counterValue % frequency
	nanoseconds := (remainder * 1000000000) / frequency

	// Write to timespec structure
	// struct timespec { time_t tv_sec; long tv_nsec; }
	// Both fields are 8 bytes on 64-bit ARM
	ptr := (*[2]uint64)(unsafe.Pointer(uintptr(timespecPtr)))
	ptr[0] = seconds
	ptr[1] = nanoseconds

	return 0 // Success
}

// ============================================================================
// Random number generation
// ============================================================================

// SyscallGetrandom generates random bytes
// Fill buffer with fake random data
//
//go:nosplit
func SyscallGetrandom(bufPtr, count, _, _, _, _ uint64) int64 {
	if count == 0 {
		return 0
	}
	buf := unsafe.Pointer(uintptr(bufPtr))
	// Fill with simple pattern (not actually random)
	for i := uint64(0); i < count; i++ {
		*(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i))) = byte(i & 0xFF)
	}
	return int64(count)
}
