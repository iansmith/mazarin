//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for yield-related functions

//go:linkname threadFindReadyForYield main.ThreadFindReady
func threadFindReadyForYield() uintptr

//go:linkname setSyscallSwitchTargetForYield main.SetSyscallSwitchTarget
func setSyscallSwitchTargetForYield(target uintptr)
