// ktimer provides an architecture-neutral interface to the hardware timer.
//
// Each architecture implements four platform functions:
//
//   PlatformTimerInit() uint32     - initialize timer hardware, return frequency in Hz
//   PlatformReadCounter() uint64   - read monotonic hardware counter (ticks)
//   PlatformRearmTimer(ticks uint64) - schedule next timer interrupt at now + ticks
//   PlatformTimerIRQ() uint32      - return the IRQ number for the timer interrupt
//
// These are defined in platform_<arch>.go and platform_<arch>.s for each
// supported architecture. See platform_arm64.go for the reference implementation.
package ktimer

// timerFrequency caches the counter frequency (read once at initialization).
// Default to 62.5MHz for safety if not initialized.
var timerFrequency uint32 = 62500000

// Init reads the counter frequency from hardware and caches it.
// Must be called during early kernel initialization.
//
//go:nosplit
func Init() {
	freq := PlatformTimerInit()
	if freq > 0 {
		timerFrequency = freq
	}
}

// Frequency returns the cached timer frequency in Hz.
//
//go:nosplit
func Frequency() uint32 {
	return timerFrequency
}

// ReadCounter reads the current monotonic hardware counter value.
// Returns raw counter ticks from the platform timer.
//
//go:nosplit
func ReadCounter() uint64 {
	return PlatformReadCounter()
}

// Rearm sets the timer to fire after the given number of ticks.
// The platform implementation converts this to an absolute deadline
// (current counter + ticks) if the hardware requires it.
//
//go:nosplit
func Rearm(ticks uint64) {
	PlatformRearmTimer(ticks)
}

// IRQNum returns the IRQ number for the timer interrupt.
// This varies by architecture and interrupt controller:
//
//	ARM64:   27 (GICv2 virtual timer PPI)
//	x86_64:  platform-dependent (LAPIC timer vector)
//	RISC-V:  5 (supervisor timer interrupt)
//
//go:nosplit
func IRQNum() uint32 {
	return PlatformTimerIRQ()
}
