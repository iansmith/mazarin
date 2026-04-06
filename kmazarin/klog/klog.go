// Package klog provides the four canonical kernel logging functions.
//
// Before the linux shepherd registers (linuxReady == 0), all output goes
// directly to the UART via polling writes. After linux is ready, output
// is routed through the soft IRQ ring so the linux shepherd can display
// it in its console window.
//
// The four functions:
//
//   - Logf(format, args...)       — stdout channel (informational)
//   - Errf(format, args...)       — stderr channel (warnings/errors)
//   - Criticalf(short, fmt, args) — short string ALWAYS to UART; long string to stderr
//   - Fatalf(short, fmt, args)    — short string ALWAYS to UART; long string to panic()
//
// Nothing else in the kernel should write to the serial port directly.
// The only exception is SyscallUartWrite which is a userspace-facing syscall.
package klog

import (
	"fmt"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

// linuxReady is 0 during early boot, 1 after the linux shepherd has
// registered on the UART soft IRQ slot. All routing decisions key off
// this single atomic.
var linuxReady uint32

// SetLinuxReady flips the switch: from this point forward, Logf/Errf
// route through the soft IRQ ring to the linux shepherd instead of
// polling the UART directly.
func SetLinuxReady() {
	atomic.StoreUint32(&linuxReady, 1)
}

// IsLinuxReady returns true after SetLinuxReady has been called.
func IsLinuxReady() bool {
	return atomic.LoadUint32(&linuxReady) != 0
}

// --- output backends (private) ---

// rawWrite sends a string directly to the UART, one byte at a time,
// polling for TX-ready. Guaranteed delivery, blocks the caller.
// This is the ONLY direct UART path in klog.
func rawWrite(s string) {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\n' {
			serial.PollWrite('\r')
		}
		serial.PollWrite(b)
	}
}

// pushStdout and pushStderr are set by the kmazarin main package via
// SetOutputFuncs to wire up the soft IRQ ring. Before they are set,
// output falls back to rawWrite.
var pushStdout func(s string)
var pushStderr func(s string)

// SetOutputFuncs registers the functions that route formatted strings
// through the soft IRQ ring to the linux shepherd. Called once from
// EnableSoftIRQConsole (or equivalent).
func SetOutputFuncs(stdout, stderr func(s string)) {
	pushStdout = stdout
	pushStderr = stderr
}

// --- the four public functions ---

// Logf writes an informational message to the stdout channel.
// Before linux: polls UART. After linux: soft IRQ ring (stdout).
func Logf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if atomic.LoadUint32(&linuxReady) == 0 || pushStdout == nil {
		rawWrite(s)
		return
	}
	pushStdout(s)
}

// Errf writes a warning/error message to the stderr channel.
// Before linux: polls UART. After linux: soft IRQ ring (stderr).
func Errf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	if atomic.LoadUint32(&linuxReady) == 0 || pushStderr == nil {
		rawWrite(s)
		return
	}
	pushStderr(s)
}

// Criticalf writes a critical kernel message. Both the short string and the
// full formatted string are ALWAYS sent directly to the UART (guaranteed
// visible even if linux is dead or the soft IRQ ring is full). The message
// is NOT also sent through the ring — direct UART is sufficient for critical
// messages and avoids double-printing.
func Criticalf(short string, format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	rawWrite(short)
	rawWrite(s)
}

// Fatalf writes a fatal message and panics. The short string is ALWAYS
// sent to the UART. The long formatted string is passed to panic().
func Fatalf(short string, format string, args ...any) {
	rawWrite(short)
	s := fmt.Sprintf(format, args...)
	rawWrite(s)
	panic(s)
}
