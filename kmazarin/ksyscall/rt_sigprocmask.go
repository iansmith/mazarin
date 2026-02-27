
package ksyscall

import "mazzy/kmazarin/serial"

// SyscallRtSigprocmask implements the rt_sigprocmask(2) syscall
// The Go runtime uses this to manage signal masks
// For now, we pretend to succeed but don't actually implement signals
//
//go:nosplit
func SyscallRtSigprocmask(how, set, oldset, sigsetsize, _, _ uint64) int64 {
	// how: SIG_BLOCK=0, SIG_UNBLOCK=1, SIG_SETMASK=2
	serial.RawUARTPuts("[SPM:h=")
	serial.RawUARTHexCompact(how)
	serial.RawUARTPuts(" s=")
	serial.RawUARTHex64(set)
	serial.RawUARTPuts(" o=")
	serial.RawUARTHex64(oldset)
	serial.RawUARTPuts("]\r\n")
	return 0
}
