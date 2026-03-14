//go:build amd64

package main

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
)

// StoreUartTxDriver is a no-op on x86_64. COM1 TX ring buffer lives
// in the serial package; no driver instance is needed.
func StoreUartTxDriver(v interface{}) {}

// uartTopHalf drains COM1 RX via I/O port reads, pushes each byte into
// topHalfUartRing as an HIDEvent, drains the COM1 TX ring buffer, and
// wakes any blocked userspace slot. Called from NonTimerIRQTopHalf when
// the IOAPIC delivers COM1's IRQ.
//
//go:nosplit
func uartTopHalf(irqNum uint32) {
	pushed := false

	// Handle RX: drain COM1 receive FIFO
	for serial.RxReady() {
		data := serial.ReadRxByte()
		ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(data)}
		if ringPush(&topHalfUartRing, ev) {
			pushed = true
		}
	}

	// Handle TX: drain COM1 TX ring buffer
	serial.COM1DrainTx()

	if pushed {
		WakeSlotForIRQ(irqNum)
	}
}
