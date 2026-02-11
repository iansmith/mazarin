//go:build arm64

package ksyscall

// Native ARM64 Linux syscall numbers for mmap/munmap.
// Used by DispatchFromOverlay which receives native numbers from the Go runtime overlay.
const (
	nativeSysMmap   = 222
	nativeSysMunmap = 215
)
