//go:build riscv64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/deviceapi"
)

// cachedPLIC holds the PLIC interrupt controller reference for RISC-V.
var cachedPLIC deviceapi.InterruptController

// initInterruptController discovers and caches the PLIC for later use.
func initInterruptController() {
	if ic, ok := device.GetInterruptController(); ok {
		cachedPLIC = ic
	}
}

// enableTimerAtController is a no-op on RISC-V.
// The timer is controlled directly via the SIE CSR (STIE bit),
// not through the PLIC. ktimer.Rearm handles this.
func enableTimerAtController() {}

// disableTimerAtController is a no-op on RISC-V.
func disableTimerAtController() {}

// enableDeviceIRQ configures and enables a VirtIO device IRQ at the PLIC.
// RISC-V uses PCI INTx (PLIC sources 32-35), level-triggered.
func enableDeviceIRQ(irq uint32, isrBase uintptr) {
	if cachedPLIC == nil {
		return
	}
	cachedPLIC.RegisterHandler(irq, func() {
		// No-op: events handled by NonTimerIRQTopHalf via assembly.
	})
	// Clear any pending INTx assertion before enabling the PLIC IRQ.
	asm.MmioRead8(isrBase)
	cachedPLIC.SetIRQPriority(irq, 0xA0)
	cachedPLIC.SetIRQTarget(irq, 0x01)
	cachedPLIC.EnableIRQ(irq)
}

// enableBlockDeviceIRQ configures and enables the block device IRQ at the PLIC.
func enableBlockDeviceIRQ(irq uint32) {
	if cachedPLIC == nil {
		return
	}
	cachedPLIC.SetIRQPriority(irq, 0xA0)
	cachedPLIC.SetIRQTarget(irq, 0x01)
	cachedPLIC.EnableIRQ(irq)
}
