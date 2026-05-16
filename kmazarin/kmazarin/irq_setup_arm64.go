//go:build arm64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/deviceapi"
	"mazzy/kmazarin/ktimer"
)

// cachedGIC holds the GIC interrupt controller reference for ARM64.
var cachedGIC deviceapi.InterruptController

// initInterruptController discovers and caches the GIC for later use.
func initInterruptController() {
	if ic, ok := device.GetInterruptController(); ok {
		cachedGIC = ic
	}
}

// enableTimerAtController enables the timer IRQ at the GIC.
func enableTimerAtController() {
	if cachedGIC != nil {
		cachedGIC.EnableIRQ(ktimer.IRQNum())
	}
}

// disableTimerAtController disables the timer IRQ at the GIC.
func disableTimerAtController() {
	if cachedGIC != nil {
		cachedGIC.DisableIRQ(ktimer.IRQNum())
	}
}

// enableDeviceIRQ configures and enables a VirtIO device IRQ at the GIC.
// ARM64 uses PCI INTx (GIC SPIs 35-38), level-triggered.
func enableDeviceIRQ(irq uint32, isrBase uintptr) {
	if cachedGIC == nil {
		return
	}
	cachedGIC.RegisterHandler(irq, func() {
		// No-op: events handled by NonTimerIRQTopHalf via assembly.
	})
	// Clear any pending INTx assertion before enabling the GIC IRQ.
	// VirtIO devices assert INTx during init (DRIVER_OK triggers
	// a config change notification). Without this, the IRQ fires
	// immediately but SetTopHalfDev hasn't been called yet → IRQ storm.
	asm.MmioRead8(isrBase)
	cachedGIC.SetIRQPriority(irq, 0xA0)
	cachedGIC.SetIRQTarget(irq, 0x01)
	cachedGIC.EnableIRQ(irq)
}

// enableMSIXDeviceIRQ configures and enables a VirtIO MSI-X device IRQ at the
// GIC. The body is device-agnostic — every MSI-X-driven VirtIO PCI device on
// ARM64 uses edge-triggered delivery through GICv2m. Shared by block + net
// (formerly enableBlockDeviceIRQ; the rename reflects the lack of any
// block-specific content).
func enableMSIXDeviceIRQ(irq uint32) {
	if cachedGIC == nil {
		return
	}
	cachedGIC.SetIRQPriority(irq, 0xA0)
	cachedGIC.SetIRQTarget(irq, 0x01)
	cachedGIC.SetIRQEdgeTriggered(irq)
	cachedGIC.EnableIRQ(irq)
}
