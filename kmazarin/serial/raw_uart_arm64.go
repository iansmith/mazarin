//go:build arm64 && !test_stubs

package serial

import "mazzy/kmazarin/asm"

// PL011 UART addresses (high-memory mapped via linear map)
const (
	pl011Base = uintptr(0xFFFFFFFF09000000)
	pl011DR   = pl011Base        // Data Register
	pl011FR   = pl011Base + 0x18 // Flag Register
)

// RawUART writes a single byte to the PL011 data register.
// No polling — caller must ensure TX FIFO has space.
//
//go:nosplit
func RawUART(b byte) {
	asm.MmioWrite32(pl011DR, uint32(b))
}

// txReady returns true when the PL011 transmitter is ready.
// Checks FR register bit 5 (TXFF = TX FIFO Full); ready when clear.
//
//go:nosplit
func txReady() bool {
	return (asm.MmioRead32(pl011FR) & 0x20) == 0
}

// PollWrite polls until TX ready, then writes the byte.
//
//go:nosplit
func PollWrite(b byte) {
	for !txReady() {
	}
	RawUART(b)
}
