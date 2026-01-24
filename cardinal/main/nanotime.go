package main

import (
	"mazzy/cardinal/asm"
)

// Time system for bare-metal ARM64
//
// Uses the ARM Generic Timer hardware counter (CNTVCT_EL0) to provide
// nanosecond-precision time since boot.

// nanotime returns the current time in nanoseconds since boot.
//
// This reads the ARM Generic Timer hardware counter (CNTVCT_EL0) and
// converts it to nanoseconds using the timer frequency.
// At 62.5 MHz: each tick = 16 nanoseconds.
//
// This function is safe to call from interrupt context (nosplit).
//
//go:nosplit
func nanotime() int64 {
	// Read hardware counter via asm package
	hardwareTicks := asm.ReadCntvctEl0()

	// Convert to nanoseconds
	// At 62.5 MHz: ticks * 16ns (1,000,000,000 / 62,500,000 = 16)
	return int64(hardwareTicks) * 16
}
