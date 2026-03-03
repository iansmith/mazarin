//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for LoadMaz bridge functions provided via go:linkname.

// blockForLoadMaz blocks the current thread for a pending LoadMaz request
// and returns the next thread's context pointer (0 if no thread available).
// The caller must call SetSyscallSwitchTarget with the returned pointer.
//
//go:linkname blockForLoadMaz main.BlockForLoadMaz
func blockForLoadMaz() uintptr

