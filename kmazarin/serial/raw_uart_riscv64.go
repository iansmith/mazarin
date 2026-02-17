//go:build riscv64 && !test_stubs

package serial

import "unsafe"

// 16550 UART addresses (high-memory mapped via linear map)
const (
	uart16550Base = uintptr(0xFFFFFFFF10000000)
	uart16550THR  = uart16550Base     // Transmitter Holding Register
	uart16550LSR  = uart16550Base + 5 // Line Status Register
)

// RawUART writes a single byte to the 16550 THR register.
// No polling — caller must ensure transmitter is ready.
//
//go:nosplit
func RawUART(b byte) {
	*(*uint8)(unsafe.Pointer(uart16550THR)) = b
}

// txReady returns true when the 16550 transmitter holding register is empty.
// Checks LSR bit 5 (THRE).
//
//go:nosplit
func txReady() bool {
	return (*(*uint8)(unsafe.Pointer(uart16550LSR)) & 0x20) != 0
}

// PollWrite polls until TX ready, then writes the byte.
//
//go:nosplit
func PollWrite(b byte) {
	for !txReady() {
	}
	RawUART(b)
}
