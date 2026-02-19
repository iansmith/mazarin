//go:build amd64 && !test_stubs

package main

// x86_64-specific assembly function declarations.
// Implementations are in gic_amd64.s and runtime_amd64.s.

// ReadTSC reads the Time Stamp Counter via RDTSC.
// Returns the 64-bit TSC value.
//
//go:nosplit
func ReadTSC() uint64

// ReadRFLAGS reads the current RFLAGS register.
// Used to check interrupt enable state (IF flag, bit 9).
//
//go:nosplit
func ReadRFLAGS() uint64

// RearmTimerNow re-arms the LAPIC timer to fire after ~10ms.
//
//go:nosplit
func RearmTimerNow()

// DisableTimerHardware disables the LAPIC timer by masking the LVT entry.
//
//go:nosplit
func DisableTimerHardware()

