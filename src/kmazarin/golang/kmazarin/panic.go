
package main

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
