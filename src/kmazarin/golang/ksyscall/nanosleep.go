//go:build qemuvirt && aarch64

package ksyscall

// SyscallNanosleep implements the nanosleep(2) syscall
// For now, we don't actually sleep - just return immediately
// TODO: Implement proper sleep using timer interrupts
//
//go:nosplit
func SyscallNanosleep(req, rem, _, _, _, _ uint64) int64 {
	// For now, just return immediately (0 duration sleep)
	// TODO: Implement proper sleep using timer
	// req points to timespec struct: {tv_sec: i64, tv_nsec: i64}
	// rem would get remaining time if interrupted
	return 0
}
