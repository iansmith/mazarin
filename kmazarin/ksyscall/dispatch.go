package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/shared/constants"
	"mazzy/shared/mazzy"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// SyscallHandler is the type for all syscall handler functions
// Takes 6 arguments (x0-x5) and returns a result (x0)
type SyscallHandler func(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64

// syscallTable is the dispatch table for all syscalls.
// Indexed by SysID (platform-independent), nil entries are unimplemented.
// Each arch's translateSyscallNum() maps native Linux numbers to SysID.
// IMPORTANT: Initialized at package level so it's available before init() runs.
var syscallTable = [NumSyscallIDs]SyscallHandler{
	SysIDIoSetup:          SyscallIoSetup,          // io_setup
	SysIDEventfd:          SyscallEventfd,          // eventfd2
	SysIDEpollCreate:      SyscallEpollCreate,      // epoll_create1
	SysIDEpollCtl:         SyscallEpollCtl,         // epoll_ctl
	SysIDEpollPwait:       SyscallEpollPwait,       // epoll_pwait
	SysIDFcntl:            SyscallFcntl,            // fcntl
	SysIDOpenat:           SyscallOpenat,           // openat
	SysIDClose:            SyscallClose,            // close
	SysIDRead:             SyscallRead,             // read
	SysIDWrite:            SyscallWrite,            // write
	SysIDExit:             SyscallExit,             // exit
	SysIDExitGroup:        SyscallExitGroup,        // exit_group
	SysIDSetTidAddress:    SyscallSetTidAddress,    // set_tid_address
	SysIDFutex:            SyscallFutex,            // futex
	SysIDNanosleep:        SyscallNanosleep,        // nanosleep
	SysIDClockGettime:     SyscallClockGettime,     // clock_gettime
	SysIDSchedGetaffinity: SyscallSchedGetaffinity, // sched_getaffinity
	SysIDSchedYield:       SyscallSchedYield,       // sched_yield
	SysIDKill:             SyscallKill,             // kill
	SysIDTkill:            SyscallTkill,            // tkill
	SysIDTgkill:           SyscallTgkill,           // tgkill
	SysIDSigaltstack:      SyscallSigaltstack,      // sigaltstack
	SysIDRtSigaction:      SyscallRtSigaction,      // rt_sigaction
	SysIDRtSigprocmask:    SyscallRtSigprocmask,    // rt_sigprocmask
	SysIDArchPrctl:        SyscallArchPrctl,        // arch_prctl (x86_64 TLS setup)
	SysIDPrctl:            SyscallPrctl,            // prctl
	SysIDGetpid:           SyscallGetpid,           // getpid
	SysIDGettid:           SyscallGettid,           // gettid
	SysIDSchedSetaffinity: SyscallSchedSetaffinity, // sched_setaffinity
	SysIDBrk:              SyscallBrk,              // brk
	SysIDMunmap:           SyscallMunmap,           // munmap
	SysIDClone:            SyscallClone,            // clone
	SysIDMmap:             SyscallMmap,             // mmap
	SysIDMprotect:         SyscallMprotect,         // mprotect
	SysIDMadvise:          SyscallMadvise,          // madvise
	SysIDPrlimit64:        SyscallPrlimit64,        // prlimit64
	SysIDGetrandom:        SyscallGetrandom,        // getrandom
	SysIDRtSigreturn:      SyscallRtSigreturn,      // rt_sigreturn
	SysIDGetitimer:        SyscallGetitimer,        // getitimer
}

// DispatchSyscall is called from assembly exception handler
// Dispatches to the appropriate syscall handler based on syscall number
//
//go:nosplit
//go:noinline
func DispatchSyscall(syscallNum uint64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// Track SVCs for diagnostics
	atomic.AddUint64(&TotalSVCCount, 1)
	tid := GetCurrentThreadTID()
	if tid < 10 {
		atomic.AddUint64(&KernelSVCCount, 1)
	} else {
		atomic.AddUint64(&shepherdSyscallCount, 1)
	}
	// Per-SID SVC counter for epoch diagnostics
	sid := getCurrentThreadSID()
	if sid >= 0 && sid < int16(len(SVCCountBySID)) {
		// Modulo is a no-op after the guard but lets the SSA prover chain
		// the index fact, eliminating panicBounds64's 160-byte frame from
		// the IRQ entry chain.
		atomic.AddUint64(&SVCCountBySID[uint(sid)%uint(len(SVCCountBySID))], 1)
	}
	// Per-syscall-number counter for SID 0 (kernel threads).
	if sid == 0 {
		atomic.AddUint64(&SID0SyscallCounts[uint(syscallNum)%uint(len(SID0SyscallCounts))], 1)
	}

	// Record entry time for kernel time accounting
	entryTick := kirq.ReadCounterValue()

	var result int64

	// Check for Mazzy syscalls first (1000+)
	if syscallNum >= mazzy.MazzySyscallBase {
		result = dispatchMazzySyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
	} else {
		// Translate native Linux syscall number to platform-independent SysID
		sysID := translateSyscallNum(syscallNum)
		if sysID == SysIDInvalid || sysID >= NumSyscallIDs {
			klog.Errf("[UNK:%x]\n", syscallNum)
			result = -38 // ENOSYS
		} else {
			// Modulo is a no-op after the guard above but lets the SSA prover
			// chain the index fact through every array access below, eliminating
			// runtime.panicBounds64's 160-byte frame from the IRQ entry chain.
			sysIdx := uint(sysID) % uint(NumSyscallIDs)
			// Per-SysID counter for all shepherd syscalls.
			atomic.AddUint64(&SysIDCounts[sysIdx], 1)
			// Check if this syscall is delegated to a userspace shepherd.
			// Magic fds (epoll instance, eventfd) are never delegated — the linux
			// shepherd doesn't know about them and would return errors.
			callerSID := getCurrentThreadSID()
			if IsDelegated(sysID, callerSID) && !IsMagicFdSyscall(sysID, arg0) {
				atomic.AddUint64(&SysIDDelegated[sysIdx], 1)
				result = DelegateSyscall(sysID, arg0, arg1, arg2, arg3, arg4, arg5)
			} else {
				handler := syscallTable[sysIdx]
				if handler == nil {
					klog.Errf("[NIL:%x]\n", syscallNum)
					result = -38 // ENOSYS
				} else {
					// Call the handler
					if sysID == SysIDNanosleep && sid == 0 {
						atomic.AddUint64(&NanosleepDispatchedSID0, 1)
					}
					result = handler(arg0, arg1, arg2, arg3, arg4, arg5)
				}
			}
		}
	}

	// Record exit time and accumulate kernel time
	exitTick := kirq.ReadCounterValue()
	kirq.AddKernelSyscallTicks(exitTick - entryTick)

	return result
}

// printKernelTimeStats prints kernel time accounting stats
func printKernelTimeStats() {
	printThreadStateSummary()
}

//go:linkname printThreadStateSummary main.PrintThreadStateSummary
func printThreadStateSummary()

// dispatchMazzySyscall handles Mazzy-specific syscalls (1000+)
//
//go:nosplit
func dispatchMazzySyscall(syscallNum uint64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	idx := syscallNum - mazzy.MazzySyscallBase
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
	// Handle mmap and munmap with early-boot code path.
	// These are the only syscalls called during Go runtime init.
	// Uses arch-specific constants (nativeSysMmap/nativeSysMunmap)
	// defined in dispatch_mmap_{arch}.go.
	switch num {
	case nativeSysMmap:
		return earlyMmap(uint64(a1), uint64(a2), uint64(a3), uint64(a4))
	case nativeSysMunmap:
		return 0
	default:
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
