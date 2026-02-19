//go:build amd64 && !test_stubs

package main

import "mazzy/kmazarin/serial"

// com1IOAPICInput is the IOAPIC input number for COM1 (ISA IRQ 4).
const com1IOAPICInput uint32 = 4

// initCOM1Uart enables COM1 RX interrupts and wires the IOAPIC to deliver
// them through the device IRQ path (vector 36 → handle_device_irq →
// NonTimerIRQTopHalf → uartTopHalf). Called during kernel init.
func initCOM1Uart() {
	serial.EnableRxInterrupt()
	SetupUartSoftIRQ(com1IOAPICInput)
	if cachedIC != nil {
		cachedIC.EnableIRQ(com1IOAPICInput)
	}
}
