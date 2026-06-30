
package main

import "mazzy/kmazarin/serial"

// excInFatalDump is a nested-fault guard for the assembly exception dumper
// (exceptions_arm64.s). It is set to 1 once a fatal (unhandled) exception dump
// begins. If the dumper itself faults (e.g. a bad SP/frame pointer), the
// re-entered handler sees this flag set and halts with a "!!DBLFLT" report
// instead of recursing into a silent double-fault wedge. It is never reset:
// any path that sets it is already halting the system.
var excInFatalDump uint64

// KernelPanic prints an error message directly to console and hangs the system.
// This is used when something goes critically wrong and we cannot continue.
// Uses serial.RawUARTPuts for direct, nosplit-safe UART output.
//
//go:nosplit
func KernelPanic(msg string) {
	serial.RawUARTPuts("\r\n*** KERNEL PANIC ***\r\n")
	serial.RawUARTPuts(msg)
	serial.RawUARTPuts("\r\n")

	// Halt the system
	Exit()
}
