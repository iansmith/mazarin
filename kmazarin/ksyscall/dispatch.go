
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kirq"
	"mazzy/shared/constants"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// SyscallHandler is the type for all syscall handler functions
// Takes 6 arguments (x0-x5) and returns a result (x0)
type SyscallHandler func(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64

// syscallTable is the dispatch table for all syscalls
// Indexed by syscall number, nil entries are unimplemented
// Size 512 to accommodate syscalls up to 278 (getrandom)
// IMPORTANT: Initialized at package level so it's available before init() runs
var syscallTable = [512]SyscallHandler{
	// I/O and file operations
	0:   SyscallIoSetup,     // io_setup
	19:  SyscallEventfd,     // eventfd2
	20:  SyscallEpollCreate, // epoll_create1
	21:  SyscallEpollCtl,    // epoll_ctl
	22:  SyscallEpollPwait,  // epoll_pwait
	25:  SyscallFcntl,       // fcntl
	56:  SyscallOpenat,      // openat
	57:  SyscallClose,       // close
	63:  SyscallRead,        // read
	64:  SyscallWrite,       // write
	93:  SyscallExit,        // exit
	94:  SyscallExitGroup,   // exit_group
	96:  SyscallSetTidAddress,     // set_tid_address
	98:  SyscallFutex,              // futex
	101: SyscallNanosleep,          // nanosleep
	113: SyscallClockGettime,       // clock_gettime
	123: SyscallSchedGetaffinity,   // sched_getaffinity
	124: SyscallSchedYield,         // sched_yield
	129: SyscallKill,               // kill
	130: SyscallTkill,              // tkill
	131: SyscallTgkill,             // tgkill
	132: SyscallSigaltstack,        // sigaltstack
	134: SyscallRtSigaction,        // rt_sigaction
	135: SyscallRtSigprocmask,      // rt_sigprocmask
	167: SyscallPrctl,              // prctl
	172: SyscallGetpid,             // getpid
	178: SyscallGettid,             // gettid
	204: SyscallSchedSetaffinity,   // sched_setaffinity
	214: SyscallBrk,                // brk
	215: SyscallMunmap,             // munmap
	220: SyscallClone,              // clone
	222: SyscallMmap,               // mmap
	226: SyscallMprotect,           // mprotect
	233: SyscallMadvise,            // madvise
	261: SyscallPrlimit64,          // prlimit64
	278: SyscallGetrandom,          // getrandom
}

// DispatchSyscall is called from assembly exception handler
// Dispatches to the appropriate syscall handler based on syscall number
//
//go:nosplit
//go:noinline
func DispatchSyscall(syscallNum uint64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// NOTE: translateSyscallNum() exists for future use when the dispatch table
	// is converted to SysID indexing. For now, the table uses native Linux
	// syscall numbers directly, so no translation is needed.

	// Record entry time for kernel time accounting
	entryTick := kirq.ReadCounterValue()

	var result int64

	// Check for Mazzy syscalls first (1000+)
	if syscallNum >= MazzySyscallBase {
		result = dispatchMazzySyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
	} else if syscallNum >= 512 {
		result = -38 // ENOSYS
	} else {
		// Get handler from table
		handler := syscallTable[syscallNum]
		if handler == nil {
			// Return -ENOSYS for unimplemented syscalls instead of panicking.
			// This lets userspace programs continue even when they invoke
			// syscalls the kernel doesn't handle yet (e.g. dup, ioctl).
			result = -38 // ENOSYS
		} else {
			// Call the handler
			result = handler(arg0, arg1, arg2, arg3, arg4, arg5)
		}
	}

	// Record exit time and accumulate kernel time
	exitTick := kirq.ReadCounterValue()
	kirq.AddKernelSyscallTicks(exitTick - entryTick)

	// Stats printing disabled for now — use clock display to verify liveness
	// count := atomic.LoadUint64(&kirq.SyscallCount)
	// if count > 0 && count%2000 == 0 {
	// 	printKernelTimeStats()
	// }

	return result
}

// printKernelTimeStats prints kernel time accounting stats
func printKernelTimeStats() {
	timerTicks, syscallTicks, ctxSwitchTicks, totalKernelTicks := kirq.GetKernelTimeStats()
	elapsedTicks := kirq.GetElapsedTicks()
	syscallCount := atomic.LoadUint64(&kirq.SyscallCount)
	timerIRQs := atomic.LoadUint64(&kirq.TimerIRQCount)
	freq := kirq.SystemTimerFrequency
	if freq == 0 {
		freq = 62500000 // default
	}

	// Convert ticks to microseconds: ticks * 1000000 / freq
	syscallUs := syscallTicks * 1000000 / freq
	totalUs := totalKernelTicks * 1000000 / freq
	elapsedUs := elapsedTicks * 1000000 / freq
	elapsedMs := elapsedUs / 1000

	// Calculate percentages using floating point to avoid integer division truncation
	kernelPct := 0.0
	userPct := 0.0
	if elapsedUs > 0 {
		kernelPct = float64(totalUs) * 100.0 / float64(elapsedUs)
		userPct = 100.0 - kernelPct
	}

	console.KPrintf("\n[KernelTime] syscalls=%d irqs=%d elapsed=%dms\n",
		syscallCount, timerIRQs, elapsedMs)
	console.KPrintf("[KernelTime] syscall=%dus kernel=%dus (%.2f%% kernel, %.2f%% user)\n",
		syscallUs, totalUs, kernelPct, userPct)
	printThreadStateSummary()
	_ = timerTicks     // suppress unused warning
	_ = ctxSwitchTicks // suppress unused warning
}

//go:linkname printThreadStateSummary main.PrintThreadStateSummary
func printThreadStateSummary()

// dispatchMazzySyscall handles Mazzy-specific syscalls (1000+)
//
//go:nosplit
func dispatchMazzySyscall(syscallNum uint64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	idx := syscallNum - MazzySyscallBase
	if idx >= uint64(len(mazzySyscallTable)) {
		syscallPanic("Invalid Mazzy syscall number", syscallNum)
		return -1 // unreachable
	}

	handler := mazzySyscallTable[idx]
	if handler == nil {
		syscallPanic("Mazzy syscall not implemented", syscallNum)
		return -1 // unreachable
	}

	return handler(arg0, arg1, arg2, arg3, arg4, arg5)
}

// Early-boot heap allocator uses arch-specific addresses from shared/constants.
// These are canonical on both ARM64 (TTBR1) and x86_64 (48-bit paging).
const (
	earlyHeapStart = constants.KernelHeapStart
	earlyHeapEnd   = constants.KernelHeapEnd
)

// earlyBumpPointer is the early-boot allocator for mmap calls during Go runtime init.
// This is used ONLY by DispatchFromOverlay, before the full syscall infrastructure is ready.
// Initialized to earlyHeapStart at package init time (no runtime dependency).
var earlyBumpPointer uint64 = earlyHeapStart

// DispatchFromOverlay is the entry point for syscalls from the Go runtime
// when running in kernel mode. Called via linkname from the overlay's
// cgo_mmap.go mmap() and munmap() replacements.
//
// CRITICAL: This function is called during early Go runtime init, BEFORE:
// - Package init() functions have run
// - The console is set up
// - The runtime config is accessible via interface{}
//
// Therefore, for mmap and munmap, we use a completely separate early-boot
// code path that has NO dependencies on runtime features.
//
//go:nosplit
func DispatchFromOverlay(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// Handle mmap (222) and munmap (215) with early-boot code path
	// These are the only syscalls called during Go runtime init
	switch num {
	case 222: // mmap
		return earlyMmap(uint64(a1), uint64(a2), uint64(a3), uint64(a4))
	case 215: // munmap
		// During early boot, we don't actually free memory
		// Just return success (0)
		return 0
	default:
		// For all other syscalls, use the full dispatcher
		// These should only be called after runtime is fully initialized
		return DispatchSyscall(uint64(num), uint64(a1), uint64(a2), uint64(a3), uint64(a4), uint64(a5), uint64(a6))
	}
}

// earlyMmap handles mmap during early Go runtime init.
// Uses a simple bump allocator with NO console output, NO interface{} calls,
// and NO dependencies on runtime features.
//
//go:nosplit
func earlyMmap(addr, length, prot, flags uint64) int64 {
	// Zero-length mmap is invalid
	if length == 0 {
		return -22 // EINVAL
	}

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	// MAP_FIXED (0x10): must return the exact address
	if (flags & 0x10) != 0 {
		if addr == 0 {
			return -22 // EINVAL - MAP_FIXED with null address
		}
		// For MAP_FIXED, just return the requested address
		// The page fault handler will allocate physical pages on demand
		if addr >= earlyHeapStart && addr+alignedLength <= earlyHeapEnd {
			return int64(addr)
		}
		return -12 // ENOMEM
	}

	// Non-MAP_FIXED: bump allocate from early heap
	for {
		currentPtr := atomic.LoadUint64(&earlyBumpPointer)
		nextPtr := currentPtr + alignedLength

		// Check bounds
		if nextPtr < currentPtr || nextPtr > earlyHeapEnd {
			return -12 // ENOMEM
		}

		if atomic.CompareAndSwapUint64(&earlyBumpPointer, currentPtr, nextPtr) {
			return int64(currentPtr)
		}
		// CAS failed, retry
	}
}


// syscallPanic handles syscall-specific panics with the syscall number
// Uses console abstraction which provides spinlock protection
func syscallPanic(msg string, syscallNum uint64) {
	console.KWriteString("\r\n*** KERNEL PANIC ***\r\n")
	console.KWriteString(msg)
	console.KWriteString(": syscall #")
	console.KPrintHex64(syscallNum)
	console.KWriteString("\r\n")

	// Halt using Exit
	Exit()
}
