//go:build amd64 && !test_stubs

package main

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
)

// com1SyntheticIRQ is the IRQ number used to identify COM1 in the soft IRQ
// system. On x86, COM1 is ISA IRQ 4. We use the real number even though
// we're polling rather than using a real hardware interrupt.
const com1SyntheticIRQ uint32 = 4

// initCOM1Uart registers COM1 as the UART device for the soft IRQ system.
// Called during kernel init so that stdio can discover the serial device.
func initCOM1Uart() {
	SetupUartSoftIRQ(com1SyntheticIRQ)
}

// pollCOM1Uart polls COM1 for incoming data and pushes received bytes
// into topHalfUartRing as HIDEvents. Called from eventPoller.
//
//go:nosplit
func pollCOM1Uart() {
	if uartIRQNum == 0 {
		return
	}
	pushed := false
	// Drain all available bytes from COM1 RX buffer
	for serial.RxReady() {
		data := serial.ReadRxByte()
		ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(data)}
		if ringPush(&topHalfUartRing, ev) {
			pushed = true
		}
	}
	if pushed {
		WakeSlotForIRQ(uartIRQNum)
	}
}
