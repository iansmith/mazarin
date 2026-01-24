
package pl011

import (
	"mazzy/kmazarin/device"
	"mazzy/shared/serial"
)

// Serial implements a PL011 UART device driver using a ring buffer
type Serial struct {
	baseAddr  uintptr            // Physical base address of UART
	ringBuf   *serial.RingBuffer // Ring buffer for output
	irqNumber int                // IRQ number for this UART
}

// Compile-time check that Serial implements DeviceUpDown
var _ device.DeviceUpDown = (*Serial)(nil)

// NewSerial creates a new PL011 serial device
func NewSerial(baseAddr uintptr, ringBuf *serial.RingBuffer, irqNumber int) *Serial {
	return &Serial{
		baseAddr:  baseAddr,
		ringBuf:   ringBuf,
		irqNumber: irqNumber,
	}
}

// Startup initializes the PL011 UART device
// For now, assumes Cardinal already initialized the hardware
func (s *Serial) Startup() error {
	// TODO: Verify UART is initialized and functional
	// TODO: Configure GIC for UART IRQ
	// For now, hardware is already initialized by Cardinal
	return nil
}

// Shutdown cleanly shuts down the UART device
func (s *Serial) Shutdown() error {
	// TODO: Disable UART interrupts
	// TODO: Flush ring buffer
	return nil
}

// PutChar writes a single character to the serial port via ring buffer
// Locks the ring buffer, writes the character, and unlocks
//
//go:nosplit
func (s *Serial) PutChar(c byte) bool {
	defer s.ringBuf.Unlock()
	s.ringBuf.Lock()
	return s.ringBuf.Write(c)
}

// PutString writes a string to the serial port, appending \r\n
// Locks the ring buffer for the entire operation
//
//go:nosplit
func (s *Serial) PutString(str string) {
	defer s.ringBuf.Unlock()
	s.ringBuf.Lock()

	// Write each character
	for i := 0; i < len(str); i++ {
		// If buffer is full, we'll drop characters (lossy but prevents deadlock)
		s.ringBuf.Write(str[i])
	}

	// Append carriage return and newline
	s.ringBuf.Write('\r')
	s.ringBuf.Write('\n')
}
