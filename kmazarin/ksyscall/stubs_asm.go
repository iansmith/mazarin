//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for yield-related functions

//go:linkname threadFindReadyForYield main.ThreadFindReady
func threadFindReadyForYield() uintptr

//go:linkname setSyscallSwitchTargetForYield main.SetSyscallSwitchTarget
func setSyscallSwitchTargetForYield(target uintptr)

// getGPointer reads the g register (X28 on ARM64, R14 on x86_64, X27 on RISC-V).
// Returns the current goroutine pointer.
//
//go:linkname getGPointer main.GetGRegister
func getGPointer() uint64
