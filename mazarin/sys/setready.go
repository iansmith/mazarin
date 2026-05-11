package sys

import "mazzy/shared/mazzy"

// SetReady signals to the kernel that this shepherd is ready to accept
// delegated work (e.g., syscall handling). Must be called after the
// shepherd has completed initialization and registered its handlers.
func SetReady(ready bool) {
	var val uintptr
	if ready {
		val = 1
	}
	RawSyscall(mazzy.SysSetReady, val, 0, 0, 0, 0, 0)
}
