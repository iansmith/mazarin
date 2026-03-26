
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

import (
	"mazzy/shared/mazzy"
	_ "unsafe" // For go:linkname
)

// DebugPutChar writes a single character to the kernel debug output.
// Uses a direct syscall with no Go runtime locks/synchronization.
// Safe to call from busy loops without blocking.
//
//go:nosplit
func DebugPutChar(c byte) {
	RawSyscall(mazzy.SysDebugPrint, uintptr(c), 0, 0, 0, 0, 0)
}
