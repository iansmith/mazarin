
package sys

import (
	"mazzy/shared/mazzy"
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
		mazzy.SysGetTime,
		uintptr(unsafe.Pointer(&ts)),
		0, 0, 0, 0, 0,
	)

	if r1 != 0 || errno != 0 {
		return TimeSpec{}, errno
	}

	return ts, nil
}

// RawSyscall makes a raw syscall with 6 arguments (P held throughout).
// Use for non-blocking kernel calls where holding the P is acceptable.
func RawSyscall(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, errno syscall.Errno) {
	return syscall.RawSyscall6(num, a1, a2, a3, a4, a5, a6)
}

// Syscall makes a syscall with 6 arguments (P released during SVC).
// Use for blocking kernel calls — releases the P so other goroutines
// can run while this M blocks in the kernel.
func Syscall(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, errno syscall.Errno) {
	return syscall.Syscall6(num, a1, a2, a3, a4, a5, a6)
}

// RawWrite writes a single byte to the given fd using RawSyscall (no P release).
// Used for diagnostic markers that must not disturb Go runtime scheduling.
var rawWriteBuf [1]byte

func RawWrite(fd int, b byte) {
	rawWriteBuf[0] = b
	syscall.RawSyscall6(syscall.SYS_WRITE, uintptr(fd), uintptr(unsafe.Pointer(&rawWriteBuf[0])), 1, 0, 0, 0)
}
