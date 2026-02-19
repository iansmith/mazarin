//go:build amd64 && !test_stubs

package serial

// COM1 I/O ports
// THR (Transmitter Holding Register) = 0x3F8
// LSR (Line Status Register) = 0x3FD

// RawUART writes a single byte to COM1 via OUTB to port 0x3F8.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func RawUART(b byte)

// txReady returns true when COM1's transmitter holding register is empty.
// Reads LSR (port 0x3FD) bit 5 (THRE).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func txReady() bool

// PollWrite polls until TX ready, then writes the byte.
//
//go:nosplit
func PollWrite(b byte) {
	for !txReady() {
	}
	RawUART(b)
}

// RxReady returns true when COM1 has received data.
// Reads LSR (port 0x3FD) bit 0 (Data Ready).
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func RxReady() bool

// ReadRxByte reads one byte from COM1's receive buffer (port 0x3F8).
// Caller must ensure RxReady() returned true first.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func ReadRxByte() byte

// EnableRxInterrupt enables the Received Data Available interrupt on COM1
// by writing 0x01 to the IER register (port 0x3F9). After this, COM1 will
// assert its IRQ line (ISA IRQ 4 → IOAPIC input 4) when data arrives.
// Implemented in raw_uart_amd64.s.
//
//go:nosplit
func EnableRxInterrupt()
