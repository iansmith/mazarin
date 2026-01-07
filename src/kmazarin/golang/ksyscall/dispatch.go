//go:build qemuvirt && aarch64

package ksyscall

import (
	"unsafe"
)

// SyscallHandler is the type for all syscall handler functions
// Takes 6 arguments (x0-x5) and returns a result (x0)
type SyscallHandler func(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64

// syscallTable is the dispatch table for all 255 possible syscalls
// Indexed by syscall number, nil entries are unimplemented
var syscallTable [256]SyscallHandler

// init registers all implemented syscalls in the dispatch table
func init() {
	// Register syscall handlers at their syscall numbers
	syscallTable[64] = SyscallWrite             // write
	syscallTable[93] = SyscallExit              // exit
	syscallTable[96] = SyscallSetTidAddress     // set_tid_address
	syscallTable[98] = SyscallFutex             // futex
	syscallTable[101] = SyscallNanosleep        // nanosleep
	syscallTable[123] = SyscallSchedGetaffinity // sched_getaffinity
	syscallTable[135] = SyscallRtSigprocmask    // rt_sigprocmask
	syscallTable[215] = SyscallMunmap           // munmap
	syscallTable[220] = SyscallClone            // clone
	syscallTable[222] = SyscallMmap             // mmap
	// Note: raisesig (261) is beyond our table size - it's a Cardinal internal syscall
}

// DispatchSyscall is called from assembly exception handler
// Dispatches to the appropriate syscall handler based on syscall number
//
//go:nosplit
//go:noinline
func DispatchSyscall(syscallNum uint64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	// Check if syscall number is in range
	if syscallNum >= 256 {
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

// syscallPanic handles syscall-specific panics with the syscall number
//
//go:nosplit
func syscallPanic(msg string, syscallNum uint64) {
	const uartBase = uintptr(0x09000000)

	// Print "PANIC: "
	uartPuts := func(s string) {
		for i := 0; i < len(s); i++ {
			*(*byte)(unsafe.Pointer(uartBase)) = s[i]
		}
	}

	printHex := func(val uint64) {
		hexChars := "0123456789ABCDEF"
		for i := 60; i >= 0; i -= 4 {
			nibble := (val >> i) & 0xF
			*(*byte)(unsafe.Pointer(uartBase)) = hexChars[nibble]
		}
	}

	uartPuts("\r\n*** KERNEL PANIC ***\r\n")
	uartPuts(msg)
	uartPuts(": syscall #")
	printHex(syscallNum)
	uartPuts("\r\n")

	// Halt using Exit
	Exit()
}
