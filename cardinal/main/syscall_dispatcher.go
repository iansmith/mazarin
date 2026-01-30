package main

import "unsafe"

// ARM64 Linux syscall numbers
const (
	_SYS_read              = 63
	_SYS_write             = 64
	_SYS_close             = 57
	_SYS_openat            = 56
	_SYS_mmap              = 222
	_SYS_munmap            = 215
	_SYS_mprotect          = 10
	_SYS_madvise           = 233
	_SYS_brk               = 214
	_SYS_rt_sigaction      = 134
	_SYS_rt_sigprocmask    = 135
	_SYS_sched_getaffinity = 123
	_SYS_exit_group        = 94
	_SYS_futex             = 98
	_SYS_set_tid_address   = 218 // ARM64 doesn't have this; using generic
	_SYS_clock_gettime     = 113
	_SYS_gettid            = 178
	_SYS_getpid            = 172
	_SYS_tgkill            = 131
	_SYS_clone             = 220
	_SYS_exit              = 93
	_SYS_nanosleep         = 101
	_SYS_sched_yield       = 124
	_SYS_prlimit64         = 261
	_SYS_getrandom         = 278
)

// cardinalSyscallBreadcrumb tracks whether any syscall has been dispatched.
// If cardinal is truly avoiding the Go runtime, this should never be set.
var cardinalSyscallBreadcrumb uint64

// CardinalSyscallDispatch is the central syscall router for cardinal.
// All syscalls from cardinal's own Go runtime are routed through here
// via the runtime overlay that patches RawSyscall6.
// Returns result on success (>= 0) or -errno on error.
//
//go:nosplit
func CardinalSyscallDispatch(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	// Breadcrumb: record that a syscall was made.
	// If we are intentionally not using the Go runtime, seeing this
	// output means something unexpected is issuing syscalls.
	if cardinalSyscallBreadcrumb == 0 {
		uartPutsDirect("!!! CARDINAL SYSCALL BREADCRUMB: first syscall dispatched !!!\r\n")
	}
	cardinalSyscallBreadcrumb++

	switch num {
	case _SYS_mmap:
		return syscallTable.Mmap(a1, uint64(a2), int32(a3), int32(a4), int32(a5), int64(a6))

	case _SYS_munmap:
		return syscallTable.Munmap(a1, uint64(a2))

	case _SYS_madvise:
		return syscallTable.Madvise(a1, uint64(a2), int32(a3))

	case _SYS_brk:
		return syscallTable.Brk(a1)

	case _SYS_futex:
		return syscallTable.Futex(unsafe.Pointer(a1), int32(a2), uint32(a3),
			unsafe.Pointer(a4), unsafe.Pointer(a5), uint32(a6))

	case _SYS_write:
		return syscallTable.Write(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case _SYS_read:
		return syscallTable.Read(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case _SYS_openat:
		return syscallTable.Open(unsafe.Pointer(a2), int32(a3), int32(a4))

	case _SYS_close:
		return syscallTable.Close(int32(a1))

	case _SYS_mprotect:
		// No-op on bare metal — all memory is RWX
		return 0

	case _SYS_rt_sigaction:
		// No signal support on bare metal
		return 0

	case _SYS_rt_sigprocmask:
		// No signal support
		return 0

	case _SYS_sched_getaffinity:
		// Report 1 CPU
		if a2 >= 8 && a3 != 0 {
			*(*uint64)(unsafe.Pointer(a3)) = 1
			return 8
		}
		return -22 // -EINVAL

	case _SYS_set_tid_address:
		return 1 // fake TID

	case _SYS_gettid:
		return 1

	case _SYS_getpid:
		return 1

	case _SYS_clock_gettime:
		if a2 != 0 {
			*(*uint64)(unsafe.Pointer(a2)) = 0     // tv_sec
			*(*uint64)(unsafe.Pointer(a2 + 8)) = 0 // tv_nsec
			return 0
		}
		return -14 // -EFAULT

	case _SYS_clone:
		// Single-threaded bare metal — no thread creation
		return -38 // -ENOSYS

	case _SYS_nanosleep:
		// Busy-wait approximation — just return success
		return 0

	case _SYS_sched_yield:
		return 0

	case _SYS_tgkill:
		return 0

	case _SYS_prlimit64:
		// Return success with a large limit
		if a4 != 0 {
			// old rlimit: set rlim_cur and rlim_max to large values
			*(*uint64)(unsafe.Pointer(a4)) = 1024 * 1024     // rlim_cur
			*(*uint64)(unsafe.Pointer(a4 + 8)) = 1024 * 1024 // rlim_max
		}
		return 0

	case _SYS_getrandom:
		// Fill buffer with zeros (no entropy source)
		return int64(a2)

	case _SYS_exit, _SYS_exit_group:
		for {
		}

	default:
		return -38 // -ENOSYS
	}
}
