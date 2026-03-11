//go:build riscv64

package main

import (
	"mazzy/kmazarin/arch/riscv64/plic"
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/shared/constants"
	"mazzy/shared/hid"
	"sync/atomic"
)

// plicInstance holds the concrete PLIC pointer for direct use from the
// external interrupt handler. Set during kernel init after device discovery.
var plicInstance *plic.PLIC

// SetPLICInstance stores the PLIC reference for the external interrupt handler.
func SetPLICInstance(p *plic.PLIC) {
	plicInstance = p
}

// PLICDispatchIRQ is an ABI0 entry point called from handle_external_interrupt
// in exceptions_riscv64.s via GO_CALL_0_0. It tail-calls plicDispatchIRQInternal.
// Declared in abi_stubs_riscv64.s.

// plicDispatchIRQInternal handles an S-mode external interrupt.
// Claims the pending PLIC interrupt, handles UART specially for
// soft IRQ delivery to userspace, dispatches other handlers via the
// PLIC handler table, and completes the interrupt.
//
//go:nosplit
//go:noinline
func plicDispatchIRQInternal() {
	if plicInstance == nil {
		return
	}

	irq := plicInstance.Claim()
	if irq == 0 {
		return // Spurious
	}

	// UART: drain NS16550 RX FIFO to soft IRQ ring for userspace delivery.
	if irq == uartIRQNum && uartIRQNum != 0 {
		ns16550UartTopHalf(irq)
	} else if irq == topHalfKbd.irqNum && topHalfKbd.usedVA != 0 {
		virtioInputPLICTopHalf(&topHalfKbd)
	} else if irq == topHalfMouse.irqNum && topHalfMouse.usedVA != 0 {
		virtioInputPLICTopHalf(&topHalfMouse)
	} else if irq == blockIRQNum && blockIRQNum != 0 {
		// VirtIO block PCI INTx: read ISR to deassert, set IOComplete for WFI loop.
		if blockISRBase != 0 {
			_ = asm.MmioRead8(blockISRBase)
		}
		if blockIOComplete != nil {
			atomic.StoreUint32(blockIOComplete, 1)
		}
	} else {
		plicInstance.CallHandler(irq)
	}

	plicInstance.Complete(irq)
}

// virtioInputPLICTopHalf handles a VirtIO input PCI INTx interrupt from the PLIC.
// Reads the VirtIO ISR register to deassert the PCI INTx line, then delegates
// to NonTimerIRQTopHalf which drains the used ring and wakes blocked slots.
//
//go:nosplit
func virtioInputPLICTopHalf(dev *topHalfDev) {
	// Reading the ISR register clears it and deasserts PCI INTx.
	if dev.isrBase != 0 {
		_ = asm.MmioRead8(dev.isrBase)
	}
	topHalfIRQNum = uint64(dev.irqNum)
	NonTimerIRQTopHalf()
}

// NS16550 register offsets for direct MMIO access in top-half context.
const (
	ns16550_RBR    uintptr = 0x00 // Receiver Buffer Register (read)
	ns16550_LSR    uintptr = 0x05 // Line Status Register
	ns16550_LSR_DR byte    = 1    // Data Ready bit in LSR
)

// ns16550UartTopHalf drains the NS16550 RX FIFO via MMIO and pushes each
// received byte to topHalfUartRing as an HIDEvent. This delivers serial
// input to userspace via the soft IRQ slot mechanism.
//
//go:nosplit
func ns16550UartTopHalf(irqNum uint32) {
	base := uintptr(constants.KernelUartBase)

	pushed := false
	for asm.MmioRead8(base+ns16550_LSR)&ns16550_LSR_DR != 0 {
		data := asm.MmioRead8(base + ns16550_RBR)
		ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(data)}
		if ringPush(&topHalfUartRing, ev) {
			pushed = true
		}
	}

	if pushed {
		WakeSlotForIRQ(irqNum)
	}
}

// EnableSEIE enables Supervisor External Interrupt Enable (bit 9) in the
// SIE CSR. Must be called after the PLIC is initialized and handlers wired.
// Declared in abi_stubs_riscv64.s.
func EnableSEIE()

// setupExternalInterrupts configures the PLIC for external interrupt delivery.
// Called from testDeviceDiscovery after WireInterrupts succeeds.
// On RISC-V, this stores the PLIC instance for the exception handler and
// enables SEIE so external interrupts are delivered to the trap handler.
func setupExternalInterrupts() {
	ic, ok := device.GetInterruptController()
	if !ok {
		return
	}
	p, ok := ic.(*plic.PLIC)
	if !ok {
		return
	}
	SetPLICInstance(p)
	EnableSEIE()
	console.KPrintln("[PLIC] External interrupts enabled")
}
