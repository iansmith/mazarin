//go:build amd64 && !test_stubs

package ktimer

// x86_64 timer uses LAPIC one-shot mode with PIT-calibrated frequencies:
//   RDTSC            - read Time Stamp Counter (monotonic ticks)
//   LAPIC LVT Timer  - configured for one-shot mode, vector 0x30
//   LAPIC ICR         - initial count register for countdown
//
// The TSC and LAPIC timer run at DIFFERENT frequencies on x86_64.
// PlatformTimerInit calibrates both against the PIT (1.193182 MHz) and
// returns the TSC frequency. PlatformRearmTimer internally converts
// TSC-based tick counts to LAPIC timer ticks using the calibrated ratio.
//
// Timer interrupt is vector 0x30 (48) routed through LAPIC.

const platformTimerIRQ = 0x30

// Calibration variables set by PlatformTimerInit (assembly).
// PlatformRearmTimer uses these to convert TSC ticks to LAPIC timer ticks:
//   lapic_ticks = tsc_ticks * scaleNumer / scaleDenom
var scaleNumer uint64 // LAPIC ticks elapsed during PIT calibration (~10ms)
var scaleDenom uint64 // TSC ticks elapsed during PIT calibration (~10ms)

// PlatformDisableTimer stops the LAPIC timer from generating interrupts.
// Implemented in platform_amd64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the LAPIC timer vector number.
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit configures the LAPIC timer and calibrates frequencies.
// Uses PIT Channel 2 as a ~10ms reference to measure both TSC and LAPIC
// timer rates. Returns the TSC frequency in Hz.
// Implemented in platform_amd64.s.
func PlatformTimerInit() uint32

// PlatformReadCounter reads the TSC and returns the current counter value.
// Implemented in platform_amd64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer schedules the next timer interrupt after 'ticks' TSC ticks.
// Internally converts TSC ticks to LAPIC timer ticks using PIT-calibrated ratio.
// Implemented in platform_amd64.s.
func PlatformRearmTimer(ticks uint64)


