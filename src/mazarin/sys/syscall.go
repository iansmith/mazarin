
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

// Mazzy syscall numbers (numerically ordered).
// These must match the kernel-side definitions in ksyscall/mazzy.go.
const (
	sysGetTime    = 0x1000 // Get current time
	sysLaunch     = 0x1001 // Launch a priest from ELF file
	sysRun        = 0x1002 // Load a .maz program into priest's address space
	sysAllocPages = 0x1003 // Allocate pages for userspace
	sysExit       = 0x1004 // Exit program (Mazzy-specific)
	sysReap       = 0x1005 // Reap terminated program
)
