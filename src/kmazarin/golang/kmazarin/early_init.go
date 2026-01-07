//go:build qemuvirt && aarch64

package main

// EarlyInit initializes devices that must be set up before DTB scanning
// This includes:
//   - UART (direct mode for early debug output)
//   - GIC (interrupt controller)
//   - Timer (for scheduling/preemption)
//
// Called from init() before the Go runtime is fully initialized
func EarlyInit() {
	Print("[Early] Initializing critical devices...")

	// 1. UART is already working (we're using direct mode)
	Print("[Early]   UART: already initialized (direct mode)")

	// 2. GIC is already initialized by Cardinal - don't reinitialize!
	// Just enable our specific IRQs
	Print("[Early]   GIC: using Cardinal's setup")

	// 3. Enable timer interrupt (IRQ 27) in GIC
	Print("[Early]   Timer IRQ: enabling IRQ 27...")
	EnableTimerIRQ()
	Print("[Early]   Timer IRQ: IRQ 27 enabled")

	// 4. Timer is already armed by Cardinal - will fire soon
	Print("[Early]   Timer: relying on Cardinal's setup")

	// 5. RNG initialization would go here
	// TODO: Add VirtIO RNG initialization when we have VirtIO support
	Print("[Early]   RNG: not yet implemented")

	Print("[Early] Early initialization complete")
}
