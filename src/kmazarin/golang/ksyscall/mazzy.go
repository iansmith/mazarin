//go:build qemuvirt && aarch64

package ksyscall

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers (alphabetically ordered)
const (
	SysGetTime = MazzySyscallBase + 0 // 1000
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0: SyscallGetTime, // GetTime = 1000
}
