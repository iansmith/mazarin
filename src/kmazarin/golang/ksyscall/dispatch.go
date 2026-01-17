//go:build qemuvirt && aarch64

package ksyscall

import "kmazarin/console"

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
	// Check if syscall number is in range
	if syscallNum >= 512 {
		syscallPanic("Invalid syscall number", syscallNum)
		return -1 // unreachable
	}

	// Get handler from table
	handler := syscallTable[syscallNum]
	if handler == nil {
		syscallPanic("Syscall not implemented", syscallNum)
		return -1 // unreachable
	}

	// Call the handler
	return handler(arg0, arg1, arg2, arg3, arg4, arg5)
}

// DispatchFromOverlay is the entry point for syscalls from the Go runtime
// when running in kernel mode. Called via linkname from the overlay's
// internal/runtime/syscall.Syscall6 replacement.
//
// This function has the same semantics as DispatchSyscall but takes uintptr
// arguments to match the runtime's calling convention.
//
//go:nosplit
func DispatchFromOverlay(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	return DispatchSyscall(uint64(num), uint64(a1), uint64(a2), uint64(a3), uint64(a4), uint64(a5), uint64(a6))
}

// syscallPanic handles syscall-specific panics with the syscall number
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func syscallPanic(msg string, syscallNum uint64) {
	console.KWriteString("\r\n*** KERNEL PANIC ***\r\n")
	console.KWriteString(msg)
	console.KWriteString(": syscall #")
	console.KPrintHex64(syscallNum)
	console.KWriteString("\r\n")

	// Halt using Exit
	Exit()
}
