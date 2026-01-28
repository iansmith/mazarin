
package ksyscall

import (
	"mazzy/kmazarin/console"
	"unsafe"
)

// SyscallSchedGetaffinity implements the sched_getaffinity(2) syscall
// The Go runtime uses this to detect available CPUs
// For now, we report 1 CPU available
//
//go:nosplit
func SyscallSchedGetaffinity(pid, cpusetsize, mask, _, _, _ uint64) int64 {
	// Debug: print the mask address and current PID to understand who's calling
	currentPID := getCurrentThreadPIDForSpan()
	console.KWriteString("[sched_getaffinity] PID=")
	console.KPrintHex64(uint64(currentPID))
	console.KWriteString(" mask=")
	console.KPrintHex64(mask)
	console.KWriteString("\r\n")

	// Report 1 CPU available (bit 0 set in mask)
	if cpusetsize < 8 {
		return -22 // EINVAL
	}

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(mask) {
		console.KWriteString("[sched_getaffinity] EFAULT: invalid mask addr\r\n")
		return -14 // EFAULT
	}

	// Set bit 0 (CPU 0 available)
	*(*uint64)(unsafe.Pointer(uintptr(mask))) = 0x1

	return 8 // Return size of mask in bytes
}
