
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ktime"
)

// MazzyTimeSpec is the structure for GetTime results.
type MazzyTimeSpec struct {
	Seconds     uint64
	Nanoseconds uint64
}

// SyscallGetTime implements Mazzy syscall 1000.
// arg0: pointer to MazzyTimeSpec
// Returns: 0 on success, -14 (EFAULT) if pointer is NULL or kernel address
//
//go:nosplit
func SyscallGetTime(timespecPtr, _, _, _, _, _ uint64) int64 {
	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(timespecPtr) {
		return -14 // EFAULT
	}

	seconds, nanoseconds := ktime.GetTime()

	if !kmem.WriteUserUint64(uintptr(timespecPtr), seconds) {
		return -14 // EFAULT
	}
	if !kmem.WriteUserUint64(uintptr(timespecPtr+8), nanoseconds) {
		return -14 // EFAULT
	}

	return 0
}
