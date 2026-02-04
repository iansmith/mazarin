//go:build amd64 && !test_stubs

package console

// breadcrumb writes a byte directly to COM1 serial port.
// This is the low-level implementation used by all console implementations.
// Safe to call from any context, including IRQ handlers.
//
// On x86_64 QEMU: COM1 at I/O port 0x3F8 via OUTB instruction.
// Implemented in breadcrumb_amd64.s.
//
//go:nosplit
func breadcrumb(b byte)
