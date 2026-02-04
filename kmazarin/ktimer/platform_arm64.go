//go:build arm64 && !test_stubs

package ktimer

// ARM64 timer uses the Generic Timer:
//   CNTFRQ_EL0  - counter frequency register (Hz)
//   CNTVCT_EL0  - virtual counter value (monotonic ticks)
//   CNTV_CVAL_EL0 - virtual timer compare value (absolute deadline)
//   CNTV_CTL_EL0  - virtual timer control (enable/mask)
//
// Timer interrupt is PPI 27 (virtual timer) routed through GICv2.

const platformTimerIRQ = 27

// PlatformDisableTimer disables the ARM virtual timer hardware (CNTV_CTL_EL0).
// Stops the timer from generating interrupt requests.
// Implemented in platform_arm64.s.
func PlatformDisableTimer()

// PlatformTimerIRQ returns the GICv2 virtual timer PPI number.
//
//go:nosplit
func PlatformTimerIRQ() uint32 { return platformTimerIRQ }

// PlatformTimerInit reads CNTFRQ_EL0 and returns the frequency in Hz.
// Implemented in platform_arm64.s.
func PlatformTimerInit() uint32

// PlatformReadCounter reads CNTVCT_EL0 and returns the current counter value.
// Implemented in platform_arm64.s.
func PlatformReadCounter() uint64

// PlatformRearmTimer sets the next timer interrupt at current + ticks.
// Reads CNTVCT_EL0, adds ticks, writes CNTV_CVAL_EL0, enables the timer.
// Implemented in platform_arm64.s.
func PlatformRearmTimer(ticks uint64)
