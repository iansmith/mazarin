//go:build qemuvirt && aarch64

package ksyscall

import (
	"kmazarin/ktime"
	"unsafe"
)

// MazzyTimeSpec is the structure for GetTime results.
type MazzyTimeSpec struct {
	Seconds     uint64
	Nanoseconds uint64
}

// SyscallGetTime implements Mazzy syscall 1000.
// arg0: pointer to MazzyTimeSpec
// Returns: 0 on success, -14 (EFAULT) if pointer is NULL
//
//go:nosplit
func SyscallGetTime(timespecPtr, _, _, _, _, _ uint64) int64 {
	if timespecPtr == 0 {
		return -14 // EFAULT
	}

	seconds, nanoseconds := ktime.GetTime()

	ts := (*MazzyTimeSpec)(unsafe.Pointer(uintptr(timespecPtr)))
	ts.Seconds = seconds
	ts.Nanoseconds = nanoseconds

	return 0
}
