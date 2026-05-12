package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/constants"
	"sync/atomic"

	"unsafe"
)

// getCPUCountForAffinity returns the number of CPUs configured
//
//go:linkname getCPUCountForAffinity main.GetCPUCount
func getCPUCountForAffinity() uint64

// madviseDiagCount limits madvise userspace diagnostic log volume.
var madviseDiagCount int32

// ============================================================================
// User Buffer Address Validation
// ============================================================================

// isValidUserAddr checks if an address is a valid buffer address for the current context.
// For kernel threads (PID 0): accepts both kernel and userspace addresses (only rejects NULL)
// For userspace threads (shepherds): rejects NULL and kernel addresses
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
	p := proc.CurrentShepherd()

	// For kernel threads (PID 0 / nil shepherd), allow kernel addresses.
	// The Go runtime running in kmazarin uses kernel addresses (0xFFFF...)
	if p == nil {
		return true // Kernel context - any non-NULL address is valid
	}

	// For userspace (shepherd) threads, reject kernel addresses
	// Kernel addresses have the upper 16 bits set (TTBR1 space)
	if (addr & 0xFFFF000000000000) != 0 {
		return false
	}
	return true
}

// ============================================================================
// Simple stub syscalls that return constants or success
// ============================================================================

// SyscallGetpid returns the process ID (shepherd PID).
// Returns 0 for kernel threads.
//
//go:nosplit
func SyscallGetpid(_, _, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return 0
	}
	return int64(p.PID)
}

// SyscallGettid returns the thread ID
//
//go:nosplit
func SyscallGettid(_, _, _, _, _, _ uint64) int64 {
	return int64(GetCurrentThreadTID())
}

// SyscallSchedYield yields the processor to another ready thread
// If no other threads are ready, returns immediately.
//
// CRITICAL: Must check m.locks before yielding. The Go runtime sets m.locks > 0
// during critical sections (e.g., inside mallocgc, mspan.sweep). Yielding with
// locks held allows another goroutine on the same M to see inconsistent allocator
// state, causing mspan metadata corruption. This mirrors the check in the timer
// IRQ preemption path (kirq/preempt_arm64.s).
//
//go:nosplit
func SyscallSchedYield(_, _, _, _, _, _ uint64) int64 {
	atomic.AddUint64(&YieldCallCount, 1)
	// NOTE: Do NOT check m.locks here. Voluntary yields (from futex spin,
	// usleep, etc.) must always be allowed. The Go runtime holds m.locks
	// when calling futex/lock2, so blocking yield here creates a deadlock:
	// the thread waiting on a futex can never yield to the thread that would
	// release the lock. The m.locks check is only appropriate for ASYNC
	// (timer-driven) preemption, not voluntary yields.

	// If thread 0 has pending kernel dispatch work (RunShepherd/Epoll/UringConnect),
	// skip the OS-level thread switch. This lets Go's internal goroutine
	// scheduler keep cycling through goroutines on M0 until the idle loop
	// goroutine (which relays worker requests) gets scheduled.
	// Without this, goroutines on M0 doing SVC sched_yield cause thread 0
	// to be context-switched away, starving the idle loop of CPU time and
	// preventing kernel dispatch work from ever being processed.
	// Timer preemption still works, so other threads still get CPU time.
	if thread0HasPendingWork() {
		return 0
	}

	// Try to find another ready thread to switch to
	nextThread := threadFindReadyForYield()

	if nextThread != 0 {
		// Found a ready thread - yield to it
		atomic.AddUint64(&YieldSwitchCount, 1)
		setSyscallSwitchTargetForYield(nextThread)
	} else {
		atomic.AddUint64(&YieldNoReadyCount, 1)
	}

	return 0 // Success
}

// runtimeMLocks reads m.locks from the current goroutine's M structure.
// Uses the same offsets as the timer IRQ preemption check in assembly.
// Returns 0 if locks are not held (safe to yield), non-zero if held.
//
//go:nosplit
func runtimeMLocks() int32 {
	gmOff := kirq.PreemptGMOffset
	mLocksOff := kirq.PreemptMLocksOffset

	// If offsets aren't initialized yet (early boot), allow yield
	if gmOff == 0 && mLocksOff == 0 {
		return 0
	}

	// g pointer is in X28 (ARM64) / R14 (x86_64) / X27 (RISC-V)
	// Read it via assembly helper linked from main package
	gRaw := getGPointer()
	if gRaw == 0 {
		return 0
	}
	gPtr := uintptr(gRaw)

	// g.m is at g + GMOffset
	mPtr := *(*uintptr)(unsafe.Pointer(gPtr + gmOff))
	if mPtr == 0 {
		return 0
	}

	// m.locks is at m + MLocksOffset (int32)
	locks := *(*int32)(unsafe.Pointer(mPtr + mLocksOff))
	return locks
}

// SyscallSigaltstack is now in signal.go

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

// SyscallMadvise gives advice about memory usage.
// For MADV_DONTNEED and MADV_FREE on kernel heap pages, clears the PTEs
// and returns physical frames to the buddy allocator. This is critical
// for the Go runtime scavenger to reclaim unused heap memory.
// For shepherd userspace pages, clears PTEs and returns frames so the Go
// scavenger inside a shepherd can reclaim memory; Spans are preserved so
// the next page fault demand-pages a fresh zeroed page.
// For unrecognized advice, returns success (no-op).
//
//go:nosplit
func SyscallMadvise(addr, length, advice, _, _, _ uint64) int64 {
	const (
		MADV_DONTNEED = 4
		MADV_FREE     = 8
	)

	// Only act on DONTNEED/FREE — other advice is a no-op
	if advice != MADV_DONTNEED && advice != MADV_FREE {
		return 0
	}

	// Align to page boundaries
	pageSize := uint64(4096)
	alignedAddr := addr &^ (pageSize - 1)
	alignedEnd := (addr + length + pageSize - 1) &^ (pageSize - 1)
	if alignedEnd <= alignedAddr {
		return 0
	}

	heapStart := uint64(constants.KernelHeapStart)
	heapEnd := uint64(constants.KernelHeapEnd)

	if addr >= heapStart && addr+length <= heapEnd {
		// Kernel heap path: use hardware page table (current goroutine's address space).
		savedDAIF := saveAndDisableIRQs()
		for va := alignedAddr; va < alignedEnd; va += pageSize {
			pa := kmem.ReleaseKernelPage(uintptr(va))
			if pa != 0 {
				kmem.ReleasePageByPA(pa)
			}
		}
		restoreIRQs(savedDAIF)
		return 0
	}

	if addr+length <= heapStart {
		// Userspace path: look up the calling shepherd's page table explicitly.
		shepherd := proc.CurrentShepherd()
		if shepherd == nil {
			return 0
		}
		l0PA := shepherd.PageTableL0PA
		savedDAIF := saveAndDisableIRQs()
		freed := 0
		for va := alignedAddr; va < alignedEnd; va += pageSize {
			pa := kmem.UnmapUserPageWithL0(uintptr(va), l0PA)
			if pa != 0 {
				kmem.ReleasePageByPA(pa)
				freed++
			}
		}
		restoreIRQs(savedDAIF)
		// Per-madvise log removed — per-event noise.
		return 0
	}

	// Mixed or unrecognised range — no-op.
	return 0
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

// Epoll syscall handlers (SyscallEpollCreate, SyscallEventfd, SyscallEpollCtl,
// SyscallEpollPwait, IsMagicFdSyscall) have moved to epoll.go.

// SyscallFcntl performs file control operations
// Go runtime calls fcntl(fd, F_GETFD) and fcntl(fd, F_GETFL) during
// os.init() to verify and configure stdin/stdout/stderr.
//
//go:nosplit
func SyscallFcntl(fd, cmd, arg, _, _, _ uint64) int64 {
	const (
		F_DUPFD  = 0
		F_GETFD  = 1
		F_SETFD  = 2
		F_GETFL  = 3
		F_SETFL  = 4
		O_RDONLY = 0
		O_WRONLY = 1
		O_RDWR   = 2
	)
	switch cmd {
	case F_GETFD:
		if fd <= 2 {
			return 0 // No flags set, fd is valid
		}
	case F_SETFD:
		if fd <= 2 {
			return 0 // Pretend we set it
		}
	case F_GETFL:
		// Return access mode for standard file descriptors
		if fd == 0 {
			return O_RDONLY // stdin is read-only
		}
		if fd <= 2 {
			return O_WRONLY // stdout/stderr are write-only
		}
	case F_SETFL:
		if fd <= 2 {
			return 0 // Pretend we set it
		}
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

// SyscallOpenat is the fallback when no delegate handler is registered.
// Returns ENOENT for all paths. When the linux shepherd is registered and
// ready, DispatchSyscall delegates openat before reaching this stub.
//
//go:nosplit
func SyscallOpenat(dirfd, pathname, flags, mode, _, _ uint64) int64 {
	return -2 // ENOENT
}

// ============================================================================
// Signal stubs (remaining — rt_sigaction, sigaltstack, tgkill moved to signal.go)
// ============================================================================

// SyscallKill sends a signal to a process (shepherd).
// Linux: kill(pid, sig)
// Finds a thread belonging to the target PID and sets PendingSignals.
//
// Security: userspace callers cannot target kernel-reserved PIDs.
// The kernel PID (0) and any other reserved PIDs are off-limits.
//
//go:nosplit
func SyscallKill(pid, sig, _, _, _, _ uint64) int64 {
	signum := int(sig)
	// Signal 0 is a validity check — no signal is actually sent
	if signum < 0 || signum >= sigNSIG {
		return -22 // EINVAL
	}

	targetPID := int16(pid)

	// Userspace callers must not target kernel-reserved PIDs.
	if IsCurrentThreadUserspace() && targetPID < ReservedKernelPIDs() {
		return -1 // EPERM — kernel process
	}

	// Kernel callers targeting PID 0 should also be rejected —
	// the kernel signals its own threads via tgkill, not kill.
	if targetPID < ReservedKernelPIDs() {
		return -1 // EPERM
	}

	targetThread := ThreadLookupByPID(targetPID)
	if targetThread == 0 {
		return -3 // ESRCH — no such process
	}

	// Signal 0: just check existence, don't deliver
	if signum == 0 {
		return 0
	}

	SetThreadPendingSignal(targetThread, signum)
	WakeThreadForSignal(targetThread)

	return 0
}

// SyscallGetitimer returns the value of an interval timer.
// We don't support interval timers, return 0 (success, zeroed struct).
//
//go:nosplit
func SyscallGetitimer(_, _, _, _, _, _ uint64) int64 {
	return 0
}

// SyscallTkill sends a signal to a thread (deprecated in favor of tgkill).
// Forward to tgkill implementation.
//
//go:nosplit
func SyscallTkill(_, _, _, _, _, _ uint64) int64 {
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
