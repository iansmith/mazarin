package main

import (
	"mazboot/asm"
	_ "unsafe"
)

// Time system for bare-metal ARM64
//
// Uses the ARM Generic Timer hardware counter (CNTVCT_EL0) to provide
// nanosecond-precision time since boot.

var (
	// Timer frequency in Hz (read from CNTFRQ_EL0 at init)
	timerFrequency uint64

	// Nanoseconds per hardware timer tick (calculated from frequency)
	nanosPerTick int64
)

// Assembly functions to read ARM Generic Timer registers
// Implemented in src/mazboot/asm/aarch64/timer.s

//go:linkname readTimerCounter readTimerCounter
//go:nosplit
func readTimerCounter() uint64

//go:linkname getTimerFrequency getTimerFrequency
//go:nosplit
func getTimerFrequency() uint64

//go:linkname armTimer armTimer
//go:nosplit
func armTimer()

// initTime initializes the time system by reading the hardware timer frequency
// and calculating the conversion factor for nanoseconds.
//
// Must be called during boot before any time functions are used.
func initTime() {
	// Read the timer frequency from hardware
	timerFrequency = getTimerFrequency()

	if timerFrequency == 0 {
		// CNTFRQ_EL0 not set (shouldn't happen on real hardware)
		// Use QEMU default of 62.5 MHz
		timerFrequency = 62500000
		print("WARNING: CNTFRQ_EL0 returned 0, using default 62.5 MHz\r\n")
	}

	// Calculate nanoseconds per hardware tick
	// ns_per_tick = 1,000,000,000 / frequency_hz
	//
	// Example: At 62.5 MHz:
	//   1,000,000,000 / 62,500,000 = 16 nanoseconds per tick
	nanosPerTick = int64(1000000000 / timerFrequency)

	print("Time: frequency=", timerFrequency, " Hz, ")
	print("ns_per_tick=", nanosPerTick, "\r\n")

	// Initialize GIC (interrupt controller) if not already done
	print("Initializing GIC...\r\n")
	gicInit()
	print("GIC initialized\r\n")

	// Arm and enable the timer to start firing interrupts
	print("Arming timer to fire every 20ms...\r\n")
	armTimer()
	print("Timer armed and enabled\r\n")

	// Enable virtual timer interrupt (ID 27) in GIC
	print("Enabling virtual timer interrupt in GIC...\r\n")
	gicEnableInterrupt(IRQ_ID_TIMER_PPI) // IRQ_ID_TIMER_PPI = 27
	print("Timer interrupt enabled in GIC\r\n")

	// Enable IRQs globally so timer interrupts can be delivered
	print("Enabling IRQs...\r\n")
	asm.EnableIrqs()
	print("IRQs enabled\r\n")
}

// nanotime returns the current time in nanoseconds since boot.
//
// This reads the ARM Generic Timer hardware counter (CNTVCT_EL0) and
// converts it to nanoseconds using the timer frequency.
//
// This function is safe to call from interrupt context (nosplit).
//
//go:nosplit
func nanotime() int64 {
	// Read hardware counter
	hardwareTicks := readTimerCounter()

	// Convert to nanoseconds
	// At 62.5 MHz: ticks * 16ns
	return int64(hardwareTicks) * nanosPerTick
}

// nanotimeAccurate provides a more accurate conversion for high-precision timing.
// Uses 64-bit multiplication to avoid loss of precision.
//
// Note: Only use this if you need sub-microsecond accuracy, as it's slightly slower.
//
//go:nosplit
func nanotimeAccurate() int64 {
	hardwareTicks := readTimerCounter()

	// More accurate: (ticks * 1,000,000,000) / frequency
	// Uses full 64-bit precision
	return int64((hardwareTicks * 1000000000) / timerFrequency)
}
