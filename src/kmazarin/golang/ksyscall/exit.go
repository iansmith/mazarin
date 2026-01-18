
package ksyscall

import "kmazarin/console"

// SyscallExit implements the exit(2) syscall (syscall 93)
// For now, just panic - we don't have a clean shutdown mechanism yet
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	// Print exit status using console (spinlock protected)
	console.KWriteString("\r\n=== EXIT CALLED ===\r\nStatus: ")

	// Print status as decimal
	hexChars := "0123456789ABCDEF"
	tens := (status / 10) % 10
	ones := status % 10
	if tens > 0 {
		console.KWriteByte(hexChars[tens])
	}
	console.KWriteByte(hexChars[ones])
	console.KWriteString("\r\n")

	KernelPanic("Runtime called exit() during initialization")
	return 0 // unreachable
}

// SyscallExitGroup implements the exit_group(2) syscall (syscall 94)
// Same as exit for a single-threaded kernel
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	KernelPanic("Process called exit_group()")
	return 0 // unreachable
}
