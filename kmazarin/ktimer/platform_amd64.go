//go:build amd64

package ktimer

// x86_64 timer uses LAPIC one-shot mode:
//   LAPIC LVT Timer    - one-shot mode, vector 0x30
//   LAPIC Initial Count - write tick count, counts down to zero, fires vector 0x30
//   LAPIC Divide Config - set to divide-by-1 (0x0B) for maximum resolution
//
// TSC-Deadline mode was tried but QEMU TCG silently drops MSR_TSC_DEADLINE
// writes (RDMSR reads back 0), so one-shot mode is used instead.
//
// The LAPIC timer frequency is calibrated directly using PIT Channel 2
// (~10ms reference). Under QEMU TCG, the LAPIC timer runs significantly
// slower than calibrated (~200-250Hz effective rate vs ~1GHz calibrated).
// This is acceptable for kernel preemption.
//
// Timer interrupt is vector 0x30 (48) routed through LAPIC.

const platformTimerIRQ = 0x30

// PlatformDisableTimer stops the LAPIC timer from generating interrupts.
// Implemented in platform_amd64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the LAPIC timer vector number.
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit calibrates the LAPIC timer frequency via PIT Channel 2.
// Returns the LAPIC timer frequency in Hz.
// Implemented in platform_amd64.s.
func PlatformTimerInit() uint32

// PlatformReadCounter reads the TSC and returns the current counter value.
// The TSC is used as the monotonic clock source for timestamps.
// Implemented in platform_amd64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer schedules the next timer interrupt after 'ticks' LAPIC timer ticks.
// Uses LAPIC one-shot mode: writes to initial count register, timer counts down to zero.
// Implemented in platform_amd64.s.
func PlatformRearmTimer(ticks uint64)
