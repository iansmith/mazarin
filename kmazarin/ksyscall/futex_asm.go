//go:build !test_stubs

package ksyscall

import _ "unsafe" // for go:linkname

// Forward declarations for functions provided via go:linkname or assembly.

// Thread functions imported from main package via go:linkname
// These handle the actual thread state management
// Note: Functions return uintptr (thread node pointer, 0 = none)

//go:linkname ThreadBlockFutex main.ThreadBlockFutex
func ThreadBlockFutex(futexAddr uint64) uintptr

//go:linkname ThreadWakeFutex main.ThreadWakeFutex
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32

// SetSyscallSwitchTarget links to kmazarin's main package (kmazarin/golang/kmazarin/threads.go)
// Note: "main" refers to kmazarin's main package, not cardinal's, since this is part of the kmazarin binary
// target is a uintptr (thread node pointer, 0 = no switch)
//
//go:linkname SetSyscallSwitchTarget main.SetSyscallSwitchTarget
func SetSyscallSwitchTarget(target uintptr)

//go:linkname ThreadFindReady main.ThreadFindReady
func ThreadFindReady() uintptr

// WaitForInterrupt puts CPU into low-power idle state until interrupt
// Used to prevent busy-wait when futex can't block
//
//go:linkname waitForInterrupt main.WaitForInterrupt
func waitForInterrupt()

// SyscallFutex is the exported entry point called via function pointer from dispatch.go
// Implemented as an assembly stub in abi_stubs_arm64.s that tail-calls syscallFutexInternal
func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64
