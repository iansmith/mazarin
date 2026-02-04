
package ksyscall

import (
	"mazzy/kmazarin/console"
	"unsafe"

	_ "unsafe" // for go:linkname
)

// getCPUCount returns the number of CPUs configured
//
//go:linkname getCPUCount main.GetCPUCount
func getCPUCount() uint64

// SyscallSchedGetaffinity implements the sched_getaffinity(2) syscall
// The Go runtime uses this to detect available CPUs
// Reports the actual number of configured CPUs for SMP support
//
//go:nosplit
func SyscallSchedGetaffinity(pid, cpusetsize, mask, _, _, _ uint64) int64 {
	if cpusetsize < 8 {
		return -22 // EINVAL
	}

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(mask) {
		console.KWriteString("[sched_getaffinity] EFAULT: invalid mask addr\r\n")
		return -14 // EFAULT
	}

	// Get actual CPU count and build affinity mask
	// For N CPUs, set bits 0 through N-1
	cpuCount := getCPUCount()
	if cpuCount == 0 {
		cpuCount = 1 // Fallback to 1 CPU
	}
	if cpuCount > 64 {
		cpuCount = 64 // Max 64 CPUs in a uint64 mask
	}

	// Build mask: for 4 CPUs, mask = 0xF (bits 0-3 set)
	var affinityMask uint64
	if cpuCount == 64 {
		affinityMask = ^uint64(0) // All bits set
	} else {
		affinityMask = (uint64(1) << cpuCount) - 1
	}

	*(*uint64)(unsafe.Pointer(uintptr(mask))) = affinityMask

	return 8 // Return size of mask in bytes
}
