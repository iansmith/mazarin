
package sys

import (
	"syscall"
	"unsafe"
)

// TimeSpec holds time returned by GetTime.
type TimeSpec struct {
	Seconds     uint64
	Nanoseconds uint64
}

// GetTime returns the current time as seconds and nanoseconds since Unix epoch.
// The kernel reads the RTC once at boot; subsequent calls derive time from tick counter.
func GetTime() (TimeSpec, error) {
	var ts TimeSpec

	r1, _, errno := syscall.RawSyscall6(
		sysGetTime,
		uintptr(unsafe.Pointer(&ts)),
		0, 0, 0, 0, 0,
	)

	if r1 != 0 || errno != 0 {
		return TimeSpec{}, errno
	}

	return ts, nil
}

// RawSyscall makes a raw syscall with 6 arguments.
// This is used by priest to forward Mazzy syscalls to the kernel.
func RawSyscall(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, errno syscall.Errno) {
	return syscall.RawSyscall6(num, a1, a2, a3, a4, a5, a6)
}
