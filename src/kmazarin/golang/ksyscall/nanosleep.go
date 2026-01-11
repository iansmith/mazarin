//go:build qemuvirt && aarch64

package ksyscall

// SyscallNanosleep implements the nanosleep(2) syscall
// Yields to another ready thread if available
// NOTE: We don't actually sleep for the requested duration yet - we just yield.
// This prevents busy-wait loops when Go's runtime calls nanosleep to idle.
//
//go:nosplit
func SyscallNanosleep(req, rem, _, _, _, _ uint64) int64 {
	// req points to timespec struct: {tv_sec: i64, tv_nsec: i64}
	// rem would get remaining time if interrupted
	// For now, we ignore the requested sleep duration and just yield

	// Try to find another ready thread to switch to
	nextThread := ThreadFindReady()

	if nextThread >= 0 {
		// Found a ready thread - yield to it
		SetSyscallSwitchTarget(nextThread)
	}
	// else: No other ready thread, return immediately and keep running

	// Return 0 (success - slept for 0 time)
	return 0
}
