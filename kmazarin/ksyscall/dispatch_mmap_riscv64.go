package ksyscall

// Native RISC-V Linux syscall numbers for mmap/munmap.
// RISC-V uses the same numbers as ARM64.
// Used by DispatchFromOverlay which receives native numbers from the Go runtime overlay.
const (
	nativeSysMmap   = 222
	nativeSysMunmap = 215
)
