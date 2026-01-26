package main

import "unsafe"

// Syscall numbers for x86_64 Linux
const (
	SYS_read       = 0
	SYS_write      = 1
	SYS_open       = 2
	SYS_close      = 3
	SYS_mmap       = 9
	SYS_munmap     = 11
	SYS_exit_group = 231
	SYS_futex      = 202
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
		return DiplomatMmap(a1, uint64(a2), int32(a3), int32(a4), int32(a5), int64(a6))

	case SYS_munmap:
		// munmap(addr uintptr, length uint64) int64
		// For now, we don't free UEFI memory during boot - just return success
		return 0

	case SYS_futex:
		// futex(uaddr, op, val, timeout, uaddr2, val3)
		return int64(DiplomatFutex(unsafe.Pointer(a1), int32(a2), uint32(a3),
			unsafe.Pointer(a4), unsafe.Pointer(a5), uint32(a6)))

	case SYS_write:
		// write(fd int32, buf unsafe.Pointer, count uint64) int64
		return DiplomatWrite(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case SYS_read:
		// read(fd int32, buf unsafe.Pointer, count uint64) int64
		return DiplomatRead(int32(a1), unsafe.Pointer(a2), uint64(a3))

	case SYS_open:
		// open(pathname, flags, mode) int64
		return DiplomatOpen(unsafe.Pointer(a1), int32(a2), int32(a3))

	case SYS_close:
		// close(fd int32) int64
		return DiplomatClose(int32(a1))

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
