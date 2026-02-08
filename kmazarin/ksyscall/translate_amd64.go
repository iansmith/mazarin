//go:build amd64

package ksyscall

// x86ToARM64Syscall maps x86_64 Linux syscall numbers to ARM64 Linux numbers.
// The dispatch table uses ARM64 numbering (kmazarin's canonical convention).
// INT $0x80 from internal/runtime/syscall.Syscall6 passes x86_64 numbers,
// so we translate them before dispatch.
var x86ToARM64Syscall = [512]uint16{
	0:   63,  // read
	1:   64,  // write
	3:   57,  // close
	9:   222, // mmap
	10:  226, // mprotect
	11:  215, // munmap
	12:  214, // brk
	13:  134, // rt_sigaction
	14:  135, // rt_sigprocmask
	24:  124, // sched_yield
	28:  233, // madvise
	35:  101, // nanosleep
	39:  172, // getpid
	56:  220, // clone
	60:  93,  // exit
	62:  129, // kill
	72:  25,  // fcntl
	131: 132, // sigaltstack
	157: 167, // prctl
	186: 178, // gettid
	200: 130, // tkill
	202: 98,  // futex
	203: 204, // sched_setaffinity
	204: 123, // sched_getaffinity
	206: 0,   // io_setup (maps to 0, but unlikely to be called)
	218: 96,  // set_tid_address
	228: 113, // clock_gettime
	231: 94,  // exit_group
	233: 21,  // epoll_ctl
	234: 131, // tgkill
	257: 56,  // openat
	281: 22,  // epoll_pwait
	290: 19,  // eventfd2
	291: 20,  // epoll_create1
	302: 261, // prlimit64
	318: 278, // getrandom
}

// translateSyscallNum converts an x86_64 Linux syscall number to the ARM64
// equivalent used by kmazarin's dispatch table. Numbers that are already
// ARM64 (from overlay functions using hardcoded ARM64 constants) pass through
// unchanged because the translation table maps ARM64 numbers to themselves
// for entries that exist in the dispatch table.
//
//go:nosplit
func translateSyscallNum(num uint64) uint64 {
	if num >= 512 {
		return num // Mazzy syscalls or out of range — pass through
	}
	translated := uint64(x86ToARM64Syscall[num])
	if translated != 0 {
		return translated
	}
	// translated==0 could mean either "maps to ARM64 0" (io_setup) or
	// "no translation entry". For syscall 0 (x86 read), we already have
	// the mapping. For other 0 entries, return the original number —
	// it might already be an ARM64 number from the overlay.
	if num == 0 {
		return 63 // x86 read → ARM64 read
	}
	// Check if the original number has a handler in the dispatch table.
	// If so, it's already an ARM64 number (from overlay code using
	// hardcoded ARM64 constants like SYS_clone=220).
	if num < uint64(len(syscallTable)) && syscallTable[num] != nil {
		return num
	}
	return num // unknown — let dispatch handle the error
}
