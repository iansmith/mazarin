//go:build qemuvirt && aarch64

package kirq

import "kmazarin/console"

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

// IRQHandlerCanPreempt is the type for handlers that can trigger goroutine preemption
// Takes IRQ number, exception frame pointer, and returns preemption info
type IRQHandlerCanPreempt func(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo

// irqTableSimple is the dispatch table for simple IRQ handlers
var irqTableSimple [1020]IRQHandlerSimple

// irqTableCanPreempt is the dispatch table for IRQ handlers that can preempt goroutines
var irqTableCanPreempt [1020]IRQHandlerCanPreempt

// RegisterHandlers registers all implemented IRQ handlers in the dispatch table
// MUST be called before EnableIRQs() to ensure handlers are ready
//
//go:nosplit
func RegisterHandlers() {
	// Register handlers that can trigger preemption (timer can preempt goroutines)
	irqTableCanPreempt[27] = TimerIRQHandlerCanPreempt

	// Note: Device-specific handlers (like UART) are now registered dynamically
	// via RegisterSimpleHandler when devices call WireInterrupts.
}

// RegisterSimpleHandler registers a simple (non-preempting) IRQ handler.
// Called by device drivers during WireInterrupts to register their handlers.
// The handler will be called from DispatchNonTimerIRQ when the IRQ fires.
//
// The handler signature func() matches deviceapi.InterruptController.RegisterHandler.
// The IRQ number is not passed since devices know which IRQ they registered for.
//
//go:nosplit
func RegisterSimpleHandler(irq uint32, handler func()) {
	if irq < 1020 {
		// Wrap the device handler to match our table signature
		irqTableSimple[irq] = func(irqNum uint64) {
			_ = irqNum // IRQ number available but most handlers don't need it
			handler()
		}
	}
}

// UnregisterHandler removes a handler for the given IRQ.
// Called when a device is closed.
//
//go:nosplit
func UnregisterHandler(irq uint32) {
	if irq < 1020 {
		irqTableSimple[irq] = nil
		irqTableCanPreempt[irq] = nil
	}
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

	// Try handlers that can preempt first
	handlerCanPreempt := irqTableCanPreempt[irqNum]
	if handlerCanPreempt != nil {
		return handlerCanPreempt(irqNum, framePtr, elr, spEl0)
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

// ============================================================================
// OLD DISPATCH CODE - NO LONGER USED
// ============================================================================
// Non-timer IRQs now use pure assembly handlers (see uart_irq_arm64.s).
// This code is kept for reference but is no longer called from exceptions_arm64.s.
//
// REMOVED: Calling Go code from IRQ context is unsafe because:
//   1. We're on the exception stack (SP_EL1), not a goroutine stack
//   2. R28 (g) points to interrupted goroutine (not the handler's context)
//   3. Violates Go runtime assumptions about stack/goroutine consistency
//
// NEW APPROACH: IRQ handlers (assembly) → set flags → bottom half (goroutine)
// ============================================================================

/*
// CurrentIRQNum holds the IRQ number for assembly-to-Go dispatch.
// Set by assembly before calling DispatchNonTimerIRQ.
// Using a global avoids ABI complexity for cross-package calls.
var CurrentIRQNum uint64

// DispatchNonTimerIRQ is the ABI0 entry point for assembly.
// Forward declaration - implementation is via tail-call stub in dispatch_arm64.s.
func DispatchNonTimerIRQ()

// dispatchNonTimerIRQInternal is the actual implementation.
// Called via tail-call from the assembly stub.
// Reads the IRQ number from CurrentIRQNum global.
// This function does NOT support preemption - only timer IRQs can preempt.
//
//go:nosplit
//go:noinline
func dispatchNonTimerIRQInternal() {
	irqNum := CurrentIRQNum

	// Check if IRQ number is in range
	if irqNum >= 1020 {
		irqPanic("Invalid IRQ number", irqNum)
		return
	}

	// Try simple handler (non-timer IRQs don't preempt)
	handler := irqTableSimple[irqNum]
	if handler != nil {
		handler(irqNum)
		return
	}

	// No handler registered - this is an error
	irqPanic("Unhandled IRQ", irqNum)
}
*/

// irqPanic handles IRQ-specific panics with the IRQ number
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func irqPanic(msg string, irqNum uint64) {
	console.WriteString("\r\n*** KERNEL PANIC ***\r\n")
	console.WriteString(msg)
	console.WriteString(": IRQ #")
	console.PrintHex64(irqNum)
	console.WriteString("\r\n")

	// Halt using Exit
	Exit()
}
