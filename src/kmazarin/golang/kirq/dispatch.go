//go:build qemuvirt && aarch64

package kirq

import "unsafe"

// PreemptInfo contains call injection information for async preemption
// Returned by IRQ handlers that want to trigger preemption
type PreemptInfo struct {
	NewELR    uint64 // New ELR_EL1 (asyncPreempt address)
	NewSP     uint64 // New SP_EL0 (adjusted stack)
	NewLR     uint64 // New LR (interrupted PC)
	DoPreempt bool   // True if preemption should occur
}

// IRQHandlerSimple is the type for simple interrupt handlers that don't preempt
// Takes the IRQ number and returns nothing
type IRQHandlerSimple func(irqNum uint64)

// IRQHandlerPreemptable is the type for handlers that can trigger preemption
// Takes IRQ number, exception frame pointer, and returns preemption info
type IRQHandlerPreemptable func(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo

// irqTableSimple is the dispatch table for simple IRQ handlers
var irqTableSimple [1020]IRQHandlerSimple

// irqTablePreemptable is the dispatch table for preemptable IRQ handlers
var irqTablePreemptable [1020]IRQHandlerPreemptable

// init registers all implemented IRQ handlers in the dispatch table
func init() {
	// Register preemptable handlers (timer can trigger preemption)
	irqTablePreemptable[27] = TimerIRQHandlerPreemptable

	// Simple handlers would go in irqTableSimple
	// irqTableSimple[33] = HandlePL011  // PL011 UART (typically SPI 1, IRQ 33)
}

// DispatchIRQ is called from assembly IRQ exception handler
// Dispatches to the appropriate IRQ handler based on interrupt number
// Returns preemption info if the handler wants to preempt
//
//go:nosplit
//go:noinline
func DispatchIRQ(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	// Check if IRQ number is in range
	if irqNum >= 1020 {
		irqPanic("Invalid IRQ number", irqNum)
		return PreemptInfo{} // unreachable
	}

	// Try preemptable handler first
	handlerPreempt := irqTablePreemptable[irqNum]
	if handlerPreempt != nil {
		return handlerPreempt(irqNum, framePtr, elr, spEl0)
	}

	// Try simple handler
	handlerSimple := irqTableSimple[irqNum]
	if handlerSimple != nil {
		handlerSimple(irqNum)
		return PreemptInfo{} // No preemption
	}

	// No handler registered
	irqPanic("IRQ not implemented", irqNum)
	return PreemptInfo{} // unreachable
}

// irqPanic handles IRQ-specific panics with the IRQ number
//
//go:nosplit
func irqPanic(msg string, irqNum uint64) {
	const uartBase = uintptr(0xFFFFFFFF09000000)

	// Print "PANIC: "
	uartPuts := func(s string) {
		for i := 0; i < len(s); i++ {
			*(*byte)(unsafe.Pointer(uartBase)) = s[i]
		}
	}

	printHex := func(val uint64) {
		hexChars := "0123456789ABCDEF"
		for i := 60; i >= 0; i -= 4 {
			nibble := (val >> i) & 0xF
			*(*byte)(unsafe.Pointer(uartBase)) = hexChars[nibble]
		}
	}

	uartPuts("\r\n*** KERNEL PANIC ***\r\n")
	uartPuts(msg)
	uartPuts(": IRQ #")
	printHex(irqNum)
	uartPuts("\r\n")

	// Halt using Exit
	Exit()
}
