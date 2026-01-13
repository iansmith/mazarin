//go:build qemuvirt && aarch64

package ksyscall

// SyscallSetTidAddress implements the set_tid_address(2) syscall
// This is used by the Go runtime to set the clear_child_tid pointer
// For now, we ignore it and just return a fake TID
//
//go:nosplit
func SyscallSetTidAddress(tidptr, _, _, _, _, _ uint64) int64 {
	// The Go runtime calls this to set clear_child_tid
	// We don't implement thread cleanup yet, so just return a TID
	// Return 100 as a fake TID (thread 0 is main thread)
	return 100
}
