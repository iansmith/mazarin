package sys

import (
	"syscall"

	"mazzy/shared/mazzy"
)

// SubscribeDeaths registers the calling shepherd for global death notifications.
// When any shepherd dies, a ProtoDeath message is delivered to the caller's
// uring ring. Idempotent — calling multiple times is safe.
func SubscribeDeaths() error {
	r1, _, _ := syscall.RawSyscall6(
		mazzy.SysSubscribeDeaths,
		0, 0, 0, 0, 0, 0,
	)
	if r1 != 0 {
		return syscall.Errno(-r1)
	}
	return nil
}
