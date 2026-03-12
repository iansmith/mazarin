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

// thread0HasPendingWork returns true if the current thread is thread 0
// AND there is pending kernel dispatch work (LoadMaz/RunMaz/RunPriest).
// Used by SyscallSchedYield to skip OS-level thread switches when
// thread 0 has pending work, allowing Go's goroutine scheduler to
// reach the dispatcher goroutine in KernelIdleLoop.
//
//go:linkname thread0HasPendingWork main.Thread0HasPendingWork
func thread0HasPendingWork() bool
