package ksyscall

// translateSyscallNum on RISC-V is an identity function — the dispatch table
// already uses ARM64 Linux syscall numbering (kmazarin's canonical convention),
// and RISC-V Linux uses the same syscall numbers as ARM64.
//
//go:nosplit
func translateSyscallNum(num uint64) uint64 {
	return num
}
