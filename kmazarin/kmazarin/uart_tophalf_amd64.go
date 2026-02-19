//go:build amd64 && !test_stubs

package main

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
)

// uartTopHalf drains COM1 RX via I/O port reads, pushes each byte into
// topHalfUartRing as an HIDEvent, and wakes any blocked userspace slot.
// Called from NonTimerIRQTopHalf when the IOAPIC delivers COM1's IRQ.
//
//go:nosplit
func uartTopHalf(irqNum uint32) {
	pushed := false
	for serial.RxReady() {
		data := serial.ReadRxByte()
		ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(data)}
		if ringPush(&topHalfUartRing, ev) {
			pushed = true
		}
	}
	if pushed {
		WakeSlotForIRQ(irqNum)
	}
}
