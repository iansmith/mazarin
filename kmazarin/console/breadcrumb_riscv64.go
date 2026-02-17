//go:build riscv64 && !test_stubs

package console

import "mazzy/kmazarin/serial"

// breadcrumb writes a byte directly to UART hardware.
// Safe to call from any context, including IRQ handlers.
//
//go:nosplit
func breadcrumb(b byte) {
	serial.PollWrite(b)
}
