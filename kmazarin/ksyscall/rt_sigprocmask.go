
package ksyscall

// SyscallRtSigprocmask implements the rt_sigprocmask(2) syscall.
// The Go runtime uses this to manage signal masks.
// For now, we pretend to succeed but don't actually mask signals.
//
//go:nosplit
func SyscallRtSigprocmask(how, set, oldset, sigsetsize, _, _ uint64) int64 {
	return 0
}
