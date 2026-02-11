//go:build amd64

package ksyscall

// Native x86_64 Linux syscall numbers for mmap/munmap.
// Used by DispatchFromOverlay which receives native numbers from the Go runtime overlay.
const (
	nativeSysMmap   = 9
	nativeSysMunmap = 11
)
