//go:build qemuvirt && aarch64

package ksyscall

import _ "unsafe" // for go:linkname

// Exit hangs the system in an infinite loop
// Implemented in panic_arm64.s
func Exit()

// getUartBase is provided by main package via go:linkname.
//
//go:linkname getUartBase main.GetUartBase
func getUartBase() uintptr

// KernelPanic forwards to the main package implementation to avoid duplication
//
//go:linkname KernelPanic main.KernelPanic
func KernelPanic(msg string)
