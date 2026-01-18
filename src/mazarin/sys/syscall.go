
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

// Mazzy syscall numbers (alphabetically ordered).
// These must match the kernel-side definitions in ksyscall/mazzy.go.
const (
	sysGetTime = 0x1000
)
