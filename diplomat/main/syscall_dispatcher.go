package main

import "unsafe"

// Syscall numbers for x86_64 Linux
const (
	SYS_read             = 0
	SYS_write            = 1
	SYS_open             = 2
	SYS_close            = 3
	SYS_mmap             = 9
	SYS_mprotect         = 10
	SYS_munmap           = 11
	SYS_rt_sigaction     = 13
	SYS_rt_sigprocmask   = 14
	SYS_sched_getaffinity = 204
	SYS_exit_group       = 231
	SYS_futex            = 202
	SYS_set_tid_address  = 218
	SYS_clock_gettime    = 228
	SYS_gettid           = 186
)

// DiplomatSyscallDispatch is the central syscall router for diplomat.
// All syscalls from the Go runtime are routed through here.
// Returns result on success (>= 0) or -errno on error.
//
//go:nosplit
func DiplomatSyscallDispatch(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	switch num {
	case SYS_mmap:
		// mmap(addr uintptr, length uint64, prot int32, flags int32, fd int32, offset int64) int64
		return syscalls.Mmap(a1, uint64(a2), int32(a3), int32(a4), int32(a5), int64(a6))

	case SYS_munmap:
		// munmap(addr uintptr, length uint64) int64
		return syscalls.Munmap(a1, uint64(a2))

	case SYS_futex:
		// futex(uaddr, op, val, timeout, uaddr2, val3)
		return syscalls.Futex(unsafe.Pointer(a1), int32(a2), uint32(a3),
			unsafe.Pointer(a4), unsafe.Pointer(a5), uint32(a6))

	case SYS_write:
		// write(fd int32, buf unsafe.Pointer, count uint64) int64
		return syscalls.Write(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case SYS_read:
		// read(fd int32, buf unsafe.Pointer, count uint64) int64
		return syscalls.Read(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case SYS_open:
		// open(pathname, flags, mode) int64
		return syscalls.Open(unsafe.Pointer(a1), int32(a2), int32(a3))

	case SYS_close:
		// close(fd int32) int64
		return syscalls.Close(int32(a1))

	case SYS_mprotect:
		// mprotect(addr, len, prot) - stub, return success
		// In UEFI, all memory is RWX by default
		return 0

	case SYS_rt_sigaction:
		// rt_sigaction - stub, no signal support in UEFI
		// Go runtime checks this, so return success to keep it happy
		return 0

	case SYS_rt_sigprocmask:
		// rt_sigprocmask - stub, no signal support
		return 0

	case SYS_sched_getaffinity:
		// sched_getaffinity - report 1 CPU
		// a1=pid, a2=cpusetsize, a3=mask pointer
		if a2 >= 8 && a3 != 0 {
			// Set first byte to 1 (CPU 0 available)
			*(*uint64)(unsafe.Pointer(a3)) = 1
			return 8 // Return size of mask
		}
		return -22 // -EINVAL

	case SYS_set_tid_address:
		// set_tid_address - stub, return fake TID
		return 1

	case SYS_gettid:
		// gettid - stub, return fake TID
		return 1

	case SYS_clock_gettime:
		// clock_gettime - stub, return fake time
		// a1=clockid, a2=*timespec
		if a2 != 0 {
			// Set timespec to {tv_sec: 0, tv_nsec: 0}
			*(*uint64)(unsafe.Pointer(a2)) = 0     // tv_sec
			*(*uint64)(unsafe.Pointer(a2 + 8)) = 0 // tv_nsec
			return 0
		}
		return -14 // -EFAULT

	case SYS_exit_group:
		// exit_group(status int32)
		// In a bootloader, exit means halt or reboot
		// For now, just infinite loop
		for {
		}

	default:
		// Unimplemented syscall - return ENOSYS
		return -38 // -ENOSYS
	}
}
