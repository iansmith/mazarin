//go:build arm64

package ksyscall

// translateSyscallNum on ARM64 is an identity function — the dispatch table
// already uses ARM64 Linux syscall numbering, and all callers (SVC from
// overlays and runtime) use ARM64 numbers natively.
//
//go:nosplit
func translateSyscallNum(num uint64) uint64 {
	return num
}
