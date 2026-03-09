package sys

// SetReady signals to the kernel that this priest is ready to accept
// delegated work (e.g., LoadFile requests). Must be called after the
// priest has completed initialization and registered its delegate handlers.
func SetReady(ready bool) {
	var val uintptr
	if ready {
		val = 1
	}
	RawSyscall(sysSetReady, val, 0, 0, 0, 0, 0)
}
