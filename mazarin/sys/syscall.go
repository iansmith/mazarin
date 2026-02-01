
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

import (
	"errors"
	"reflect"
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
	sysRegisterAsyncPreempt  = 0x1009 // Register asyncPreempt address for goroutine preemption
	sysFlushFramebuffer      = 0x100D // Flush framebuffer region to display
)

// asyncPreempt is the runtime's async preemption function.
// We use linkname to get its address for registration with the kernel.
//
//go:linkname asyncPreempt runtime.asyncPreempt
func asyncPreempt()

// RegisterAsyncPreempt registers this process's runtime.asyncPreempt address
// with the kernel, enabling goroutine-level preemption within this process.
// Should be called early in main() before spawning goroutines.
func RegisterAsyncPreempt() error {
	// Get the address of runtime.asyncPreempt using reflect
	// This gives us the actual code address of the function
	addr := reflect.ValueOf(asyncPreempt).Pointer()
	if addr == 0 {
		return errors.New("RegisterAsyncPreempt: failed to get asyncPreempt address")
	}

	r1, _, errno := RawSyscall(sysRegisterAsyncPreempt, addr, 0, 0, 0, 0, 0)
	if errno != 0 || r1 < 0 {
		return errors.New("RegisterAsyncPreempt: syscall failed")
	}
	return nil
}

// DebugPutChar writes a single character to the kernel debug output.
// Uses a direct syscall with no Go runtime locks/synchronization.
// Safe to call from busy loops without blocking.
//
//go:nosplit
func DebugPutChar(c byte) {
	RawSyscall(sysDebugPrint, uintptr(c), 0, 0, 0, 0, 0)
}
