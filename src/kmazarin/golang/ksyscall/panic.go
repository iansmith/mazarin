
package ksyscall

import _ "unsafe" // for go:linkname

// Exit hangs the system in an infinite loop
// Implemented in panic_arm64.s
func Exit()

// KernelPanic forwards to the main package implementation to avoid duplication
//
//go:linkname KernelPanic main.KernelPanic
func KernelPanic(msg string)
