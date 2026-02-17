//go:build amd64 && !test_stubs

package console

import "mazzy/kmazarin/serial"

// breadcrumb writes a byte directly to COM1 serial port.
// Safe to call from any context, including IRQ handlers.
//
//go:nosplit
func breadcrumb(b byte) {
	serial.PollWrite(b)
}
