package sys

import "syscall"

// Exit terminates the calling program with the given status code.
// This is a Mazzy syscall that notifies the kernel and shepherd.
// This function does not return.
func Exit(status int) {
	syscall.RawSyscall6(sysExit, uintptr(status), 0, 0, 0, 0, 0)
	// Does not return
	for {
		// In case we somehow return, loop forever
	}
}
