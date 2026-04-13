//go:build amd64

package ktimer

// x86_64 timer uses the TSC as the system-wide monotonic counter and the
// LAPIC timer in one-shot mode for interrupt generation.
//
// The TSC and LAPIC timer run at different frequencies. PlatformTimerInit
// calibrates both against PIT Channel 2 (~10ms reference) in a single
// measurement window and returns the TSC frequency. The LAPIC/TSC ratio
// is stored internally for PlatformRearmTimer to convert TSC ticks to
// LAPIC ticks.
//
// TSC-Deadline mode was tried but QEMU TCG silently drops MSR_TSC_DEADLINE
// writes (RDMSR reads back 0), so LAPIC one-shot mode is used instead.
//
// Timer interrupt is vector 0x30 (48) routed through LAPIC.

const platformTimerIRQ = 0x30

// lapicElapsed and tscElapsed store the raw tick counts from the PIT
// calibration window. PlatformRearmTimer uses these to convert TSC ticks
// to LAPIC ticks: lapic_ticks = tsc_ticks * lapicElapsed / tscElapsed.
// Written by PlatformTimerInit (assembly), read by PlatformRearmTimer.
var lapicElapsed uint64
var tscElapsed uint64

// PlatformDisableTimer stops the LAPIC timer from generating interrupts.
// Implemented in platform_amd64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the LAPIC timer vector number.
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit calibrates the TSC and LAPIC timer frequencies using
// PIT Channel 2. Returns the TSC frequency in Hz. Stores the LAPIC/TSC
// ratio for PlatformRearmTimer.
// Implemented in platform_amd64.s.
func PlatformTimerInit() uint32

// PlatformReadCounter reads the TSC and returns the current counter value.
// The TSC is the monotonic clock source for all kernel timing.
// Implemented in platform_amd64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer schedules the next timer interrupt after 'ticks' TSC ticks.
// Internally converts TSC ticks to LAPIC timer ticks using the PIT-calibrated
// ratio, then writes to the LAPIC initial count register (one-shot mode).
// Implemented in platform_amd64.s.
func PlatformRearmTimer(ticks uint64)
