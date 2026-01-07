//go:build qemuvirt && aarch64

package ksyscall

// SyscallRtSigprocmask implements the rt_sigprocmask(2) syscall
// The Go runtime uses this to manage signal masks
// For now, we pretend to succeed but don't actually implement signals
//
//go:nosplit
func SyscallRtSigprocmask(how, set, oldset, sigsetsize, _, _ uint64) int64 {
	// how: SIG_BLOCK=0, SIG_UNBLOCK=1, SIG_SETMASK=2
	// We don't implement signals yet, so just pretend to succeed
	// If oldset != 0, we would write the old mask there
	// For now, just return success
	return 0
}
