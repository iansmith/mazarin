
package ksyscall

import (
	"mazzy/kmazarin/console"
	"sync/atomic"
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
	// Ultra-early breadcrumb that works before console is initialized
	// Print syscall indicator
	switch syscallNum {
	case 94: // exit_group
		console.Breadcrumb('X')
	case 222: // mmap
		console.Breadcrumb('M')
	case 220: // clone
		console.Breadcrumb('C')
	case 98: // futex
		console.Breadcrumb('F')
	case 96: // set_tid_address
		console.Breadcrumb('T')
	case 123: // sched_getaffinity
		console.Breadcrumb('A')
	case 135: // rt_sigprocmask
		console.Breadcrumb('S')
	case 134: // rt_sigaction
		console.Breadcrumb('s')
	case 172: // getpid
		console.Breadcrumb('P')
	case 178: // gettid
		console.Breadcrumb('t')
	case 113: // clock_gettime
		console.Breadcrumb('G')
	case 56: // openat
		console.Breadcrumb('O')
	case 57: // close
		console.Breadcrumb('c')
	case 63: // read
		console.Breadcrumb('R')
	case 64: // write
		console.Breadcrumb('W')
	case 167: // sysinfo
		console.Breadcrumb('I')
	default:
		console.Breadcrumb('?')
	}

	// CORRUPTION DETECTOR: DISABLED - was checking wrong address (userspace instead of kernel)
	// The original check was reading from userspace address 0x180C28 via ReadUserByte,
	// but SizeToSizeClass128 is in kernel space, not userspace.
	// TODO: Implement proper kernel memory corruption detection if needed.

	// Debug: print syscall number with first arg (skip write syscalls to reduce noise)
	if syscallNum != 64 { // skip write (0x40)
		console.KWriteString("S")
		console.KPrintHex64(syscallNum)
		console.KWriteString("(")
		console.KPrintHex64(arg0)
		console.KWriteString(") ")
	}

	// Extra debug for mmap (syscall 222 = 0xDE)
	// Print all 6 arguments and result to debug the 0,0 issue
	if syscallNum == 222 {
		console.KWriteString("\r\n[MMAP] ")
		if atomic.LoadUint32(&userspaceActive) != 0 {
			console.KWriteString("US ")
		} else {
			console.KWriteString("K ")
		}
		console.KWriteString("addr=")
		printHexDigits(arg0)
		console.KWriteString(" len=")
		printHexDigits(arg1)
		console.KWriteString(" flags=")
		printHexDigits(arg3)
		// Call the handler and print result
		result := SyscallMmap(arg0, arg1, arg2, arg3, arg4, arg5)
		console.KWriteString(" -> ")
		printHexDigits(uint64(result))
		console.KWriteString("\r\n")
		return result
	}

	// Check for Mazzy syscalls first (1000+)
	if syscallNum >= MazzySyscallBase {
		return dispatchMazzySyscall(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5)
	}

	// Check if syscall number is in range for Linux syscalls
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

// Early-boot heap allocator constants (hardcoded to avoid runtime config dependency)
// These must match layout.go values exactly.
const (
	earlyHeapStart = 0xFFFF000100000000 // KernelHeapStart from layout.go
	earlyHeapEnd   = 0xFFFF100000000000 // KernelHeapEnd from layout.go
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

// printHexDigits prints 16 hex digits for a uint64 value
// Uses only KWriteByte to avoid any allocation
//
//go:nosplit
func printHexDigits(v uint64) {
	const hexChars = "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (v >> uint(i)) & 0xF
		console.KWriteByte(hexChars[nibble])
	}
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
