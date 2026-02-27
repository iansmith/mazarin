
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

import (
	_ "unsafe" // For go:linkname
)

// Mazzy syscall numbers (numerically ordered).
// These must match the kernel-side definitions in ksyscall/mazzy.go.
const (
	sysGetTime              = 0x1000 // Get current time
	sysLaunch               = 0x1001 // Launch a priest from ELF file
	sysRun                  = 0x1002 // Load a .maz program into priest's address space
	sysAllocPages           = 0x1003 // Allocate pages for userspace
	sysExit                 = 0x1004 // Exit program (Mazzy-specific)
	sysReap                 = 0x1005 // Reap terminated program
	sysDebugPrint           = 0x1006 // Debug print arguments
	sysGetFramebuffer       = 0x1007 // Get framebuffer info
	sysWaitKernelAsync      = 0x1008 // Wait for kernel async message
	sysFlushFramebuffer      = 0x100D // Flush framebuffer region to display
	sysSetTimerDeadline      = 0x100E // Set timer deadline on soft IRQ slot
	sysSetScanoutOffset      = 0x100F // Set scanout Y offset for hardware scrolling
)

// DebugPutChar writes a single character to the kernel debug output.
// Uses a direct syscall with no Go runtime locks/synchronization.
// Safe to call from busy loops without blocking.
//
//go:nosplit
func DebugPutChar(c byte) {
	RawSyscall(sysDebugPrint, uintptr(c), 0, 0, 0, 0, 0)
}
