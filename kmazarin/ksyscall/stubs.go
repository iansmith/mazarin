
package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"

	_ "unsafe" // for go:linkname
)

// getCPUCountForAffinity returns the number of CPUs configured
//
//go:linkname getCPUCountForAffinity main.GetCPUCount
func getCPUCountForAffinity() uint64

// ============================================================================
// User Buffer Address Validation
// ============================================================================

// isValidUserAddr checks if an address is a valid buffer address for the current context.
// For kernel threads (PID 0): accepts both kernel and userspace addresses (only rejects NULL)
// For userspace threads (priests): rejects NULL and kernel addresses
// This prevents userspace from tricking the kernel into writing to kernel memory,
// while allowing kmazarin's own syscalls (which use kernel addresses) to work.
//
//go:nosplit
func isValidUserAddr(addr uint64) bool {
	// Reject NULL
	if addr == 0 {
		return false
	}

	// Get current thread's PID to determine if we're in kernel or userspace context
	p := proc.CurrentPriest()

	// For kernel threads (PID 0 / nil priest), allow kernel addresses.
	// The Go runtime running in kmazarin uses kernel addresses (0xFFFF...)
	if p == nil {
		return true // Kernel context - any non-NULL address is valid
	}

	// For userspace (priest) threads, reject kernel addresses
	// Kernel addresses have the upper 16 bits set (TTBR1 space)
	if (addr & 0xFFFF000000000000) != 0 {
		return false
	}
	return true
}

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
//
//go:nosplit
func SyscallGettid(_, _, _, _, _, _ uint64) int64 {
	return int64(GetCurrentThreadTID())
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
// In SMP configuration, we accept any valid CPU mask
// The Go scheduler manages actual CPU assignment
//
//go:nosplit
func SyscallSchedSetaffinity(pid, cpusetsize, mask, _, _, _ uint64) int64 {
	// We don't enforce CPU pinning - Go's scheduler handles CPU assignment
	// Just validate the mask isn't requesting non-existent CPUs

	if cpusetsize < 8 {
		return -22 // EINVAL
	}

	// Validate user buffer address
	if !isValidUserAddr(mask) {
		return -14 // EFAULT
	}

	// Read the requested mask
	requestedMask, ok := kmem.ReadUserUint64(uintptr(mask))
	if !ok {
		return -14 // EFAULT
	}

	// Get actual CPU count
	cpuCount := getCPUCountForAffinity()
	if cpuCount == 0 {
		cpuCount = 1
	}
	if cpuCount > 64 {
		cpuCount = 64
	}

	// Build valid CPU mask
	var validMask uint64
	if cpuCount == 64 {
		validMask = ^uint64(0)
	} else {
		validMask = (uint64(1) << cpuCount) - 1
	}

	// Mask must have at least one valid CPU
	effectiveMask := requestedMask & validMask
	if effectiveMask == 0 {
		return -22 // EINVAL - no valid CPUs in mask
	}

	return 0 // Success (we accept but don't enforce CPU pinning)
}

// SyscallPrctl performs process control operations.
// Returns -EINVAL so the Go runtime's setVMAName() probe detects
// PR_SET_VMA as unsupported and permanently disables VMA naming.
// Returning 0 (success) caused ~5000 prctl calls/sec — one after every mmap.
//
//go:nosplit
func SyscallPrctl(option, _, _, _, _, _ uint64) int64 {
	return -22 // EINVAL — disables Go runtime's PR_SET_VMA naming
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
func SyscallMadvise(addr, length, advice, _, _, _ uint64) int64 {
	// Breadcrumb: confirm scavenger is calling madvise
	// advice: 4 = MADV_DONTNEED, 8 = MADV_FREE
	serial.PollWrite('A')
	serial.RawUARTHexCompact(advice)
	serial.PollWrite(':')
	serial.RawUARTHexCompact(length)
	serial.PollWrite(' ')
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
// Return -ENOENT for /proc and /sys paths (cgroup, etc.)
// This prevents the Go runtime from looping forever trying to read cgroup info
//
//go:nosplit
func SyscallOpenat(dirfd, pathname, flags, mode, _, _ uint64) int64 {
	// Check first byte of pathname to detect /proc, /sys, /dev paths
	// These don't exist in our minimal kernel environment
	if pathname != 0 {
		firstByte, ok := kmem.ReadUserByte(uintptr(pathname))
		if ok && firstByte == '/' {
			secondByte, ok2 := kmem.ReadUserByte(uintptr(pathname + 1))
			if ok2 && (secondByte == 'p' || secondByte == 's' || secondByte == 'd') {
				// Likely /proc, /sys, or /dev - return ENOENT
				return -2 // ENOENT
			}
		}
	}
	return 5 // Fake fd for other files
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

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(timespecPtr) {
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
	// Both fields are 8 bytes on 64-bit
	if !kmem.WriteUserUint64(uintptr(timespecPtr), seconds) {
		return -14 // EFAULT
	}
	if !kmem.WriteUserUint64(uintptr(timespecPtr+8), nanoseconds) {
		return -14 // EFAULT
	}

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
	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}
	// Build random data in a kernel buffer, then copy to user
	// Process in chunks to avoid large stack allocations
	remaining := count
	offset := uint64(0)
	for remaining > 0 {
		var chunk [256]byte
		n := remaining
		if n > 256 {
			n = 256
		}
		for i := uint64(0); i < n; i++ {
			chunk[i] = byte((offset + i) & 0xFF)
		}
		if !kmem.CopyToUser(uintptr(bufPtr+offset), chunk[:n]) {
			return -14 // EFAULT
		}
		offset += n
		remaining -= n
	}
	return int64(count)
}
