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
	_SYS_set_tid_address   = 218
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
// Cardinal should not be using the Go runtime, so this should never fire.
var cardinalSyscallBreadcrumb uint64

// CardinalSyscallDispatch handles syscalls from cardinal's own Go runtime
// (via the runtime overlay that patches RawSyscall6).
// Cardinal doesn't use the Go runtime, so these should never be called.
// All handlers are simple stubs.
//
//go:nosplit
func CardinalSyscallDispatch(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	if cardinalSyscallBreadcrumb == 0 {
		uartPutsDirect("\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("!!! CARDINAL SYSCALL BREADCRUMB                         !!!\r\n")
		uartPutsDirect("!!! Cardinal's Go runtime is issuing syscalls!           !!!\r\n")
		uartPutsDirect("!!! This should NOT happen — cardinal does not use the   !!!\r\n")
		uartPutsDirect("!!! Go runtime. If you see this, something is wrong.    !!!\r\n")
		uartPutsDirect("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\r\n")
		uartPutsDirect("  syscall number: ")
		uartPutHex64Direct(uint64(num))
		uartPutsDirect("\r\n")
	}
	cardinalSyscallBreadcrumb++

	switch num {
	case _SYS_mmap:
		return -12 // -ENOMEM
	case _SYS_munmap, _SYS_madvise, _SYS_mprotect:
		return 0
	case _SYS_brk:
		return 0
	case _SYS_futex:
		return 0
	case _SYS_write:
		// Write to UART for fd 1 (stdout) or 2 (stderr)
		fd := int32(a1)
		if fd == 1 || fd == 2 {
			buf := unsafe.Pointer(a2)
			count := uint64(a3)
			for i := uint64(0); i < count; i++ {
				uartPutc(*(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i))))
			}
			return int64(count)
		}
		return -9 // -EBADF
	case _SYS_read:
		return 0 // EOF
	case _SYS_openat:
		return -2 // -ENOENT
	case _SYS_close:
		return 0
	case _SYS_rt_sigaction, _SYS_rt_sigprocmask:
		return 0
	case _SYS_sched_getaffinity:
		if a2 >= 8 && a3 != 0 {
			*(*uint64)(unsafe.Pointer(a3)) = 1
			return 8
		}
		return -22 // -EINVAL
	case _SYS_set_tid_address, _SYS_gettid, _SYS_getpid:
		return 1
	case _SYS_clock_gettime:
		if a2 != 0 {
			*(*uint64)(unsafe.Pointer(a2)) = 0
			*(*uint64)(unsafe.Pointer(a2 + 8)) = 0
			return 0
		}
		return -14 // -EFAULT
	case _SYS_clone:
		return -38 // -ENOSYS
	case _SYS_nanosleep, _SYS_sched_yield, _SYS_tgkill:
		return 0
	case _SYS_prlimit64:
		if a4 != 0 {
			*(*uint64)(unsafe.Pointer(a4)) = 1024 * 1024
			*(*uint64)(unsafe.Pointer(a4 + 8)) = 1024 * 1024
		}
		return 0
	case _SYS_getrandom:
		return int64(a2)
	case _SYS_exit, _SYS_exit_group:
		for {
		}
	default:
		return -38 // -ENOSYS
	}
}
