// diplomat/main/syscall_dispatcher.go - Minimal stub for overlay linkname target
//
// The diplomat-linux overlay's RawSyscall6 references DiplomatSyscallDispatch
// via go:linkname. This stub satisfies that reference. Diplomat never actually
// issues syscalls — the Go runtime's internal calls (futex, clone, etc.) are
// handled by the assembly stubs in the overlay's sys_linux_*.s files.

package main

// DiplomatSyscallDispatch is the linkname target for the overlay's RawSyscall6.
// Never reached at runtime — diplomat doesn't use the syscall package.
//
//go:nosplit
func DiplomatSyscallDispatch(num, a1, a2, a3, a4, a5, a6 uintptr) int64 {
	return -38 // ENOSYS
}
