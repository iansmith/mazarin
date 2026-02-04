//go:build amd64 && !test_stubs

package ktimer

// x86_64 timer uses TSC Deadline Mode (LAPIC):
//   RDTSC            - read Time Stamp Counter (monotonic ticks)
//   IA32_TSC_DEADLINE (MSR 0x6E0) - absolute TSC value for next interrupt
//   LAPIC LVT Timer  - configured for TSC-Deadline mode
//
// Timer interrupt is vector 0x20 (32) routed through LAPIC.

const platformTimerIRQ = 0x20

// PlatformDisableTimer stops the LAPIC timer from generating interrupts.
// Implemented in platform_amd64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the LAPIC timer vector number.
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit configures the LAPIC timer in TSC-Deadline mode.
// Returns the TSC frequency in Hz (hardcoded to 1GHz for QEMU).
// TODO: Use CPUID leaf 0x15 to calculate actual TSC frequency.
// Implemented in platform_amd64.s.
func PlatformTimerInit() uint32

// PlatformReadCounter reads the TSC and returns the current counter value.
// Implemented in platform_amd64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer sets the next timer interrupt at current + ticks.
// Reads TSC, adds ticks, writes IA32_TSC_DEADLINE MSR.
// Implemented in platform_amd64.s.
func PlatformRearmTimer(ticks uint64)
