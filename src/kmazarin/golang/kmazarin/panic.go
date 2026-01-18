
package main

// Exit hangs the system in an infinite loop
// Implemented in panic_arm64.s
func Exit()

// KernelPanic prints an error message directly to console and hangs the system
// This is used when something goes critically wrong and we cannot continue
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func KernelPanic(msg string) {
	// Use breadcrumbs for panic output - safe from any context
	BreadcrumbString("\r\n*** KERNEL PANIC ***\r\n")
	BreadcrumbString(msg)
	BreadcrumbString("\r\n")

	// Halt the system
	Exit()
}
