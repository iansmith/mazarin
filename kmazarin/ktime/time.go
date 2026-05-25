
// Package ktime provides kernel time services.
// The RTC is read once at initialization; subsequent time queries
// derive from the cached base time plus elapsed CPU ticks.
package ktime

import (
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/kirq"
	"sync/atomic"
)

// timeState holds the cached RTC base time and tick counter.
// Time is computed as: baseSeconds + elapsed ticks converted to seconds/nanoseconds.
type timeState struct {
	initialized uint32
	baseSeconds uint64 // RTC reading at init
	baseTicks   uint64 // Tick counter at init
	frequency   uint64 // Timer frequency (Hz)
}

var state timeState

// Init reads RTC once and caches it with tick counter.
// Must be called after device discovery (device.InitFromDTB) completes.
// If no clock device is available, falls back to boot time = 0 (uptime mode).
// Returns true on success (with RTC), false if using fallback.
func Init() bool {
	if atomic.LoadUint32(&state.initialized) == 1 {
		return true
	}

	// Read tick counter FIRST (minimize drift)
	ticks := kirq.ReadCounterValue()
	freq := uint64(kirq.GetTimerFrequency())

	var seconds uint64
	clock, ok := device.GetClock()
	if ok {
		seconds, _ = clock.Now()
	}
	// If no clock device, seconds stays 0 → GetTime returns uptime

	state.baseTicks = ticks
	state.baseSeconds = seconds
	state.frequency = freq
	atomic.StoreUint32(&state.initialized, 1)

	return ok
}

// GetTime returns current time derived from RTC + elapsed ticks.
// Seconds is time since Unix epoch. Nanoseconds is the sub-second component.
// Init() must have been called before this function.
// Returns (0, 0) if not initialized.
//
// Two early-return checks below look redundant; they aren't, and the
// difference is load-bearing on ARM64. The atomic load on state.initialized
// pairs with Init's atomic.StoreUint32 release-store — it's an acquire-load
// that publishes Init's three prior plain writes (baseTicks, baseSeconds,
// frequency) to this caller's core. Without it, another core could observe
// state.frequency != 0 (Init's third write) but still see baseTicks == 0
// (write not yet visible) and return a wildly wrong time. The freq == 0
// belt-and-suspenders is kept because kmazarin/ktimer/timer.go:16 defaults
// timerFrequency to a non-zero platform value (62.5 MHz) deliberately, so
// the initialized flag is the only signal that *ktime*.Init ran — without
// it a pre-Init GetTime call would silently produce wrong-but-plausible
// times instead of (0, 0).
//
//go:nosplit
func GetTime() (seconds, nanoseconds uint64) {
	if atomic.LoadUint32(&state.initialized) == 0 {
		return 0, 0
	}

	freq := state.frequency
	if freq == 0 {
		return 0, 0
	}

	currentTicks := kirq.ReadCounterValue()
	elapsedTicks := currentTicks - state.baseTicks

	elapsedSeconds := elapsedTicks / freq
	remainderTicks := elapsedTicks % freq
	elapsedNanoseconds := (remainderTicks * 1_000_000_000) / freq

	return state.baseSeconds + elapsedSeconds, elapsedNanoseconds
}

// GetBootEpoch returns the boot epoch data for passing to userspace shepherds.
// Returns (baseSeconds, baseTicks, frequency) where:
//   - baseSeconds is Unix epoch seconds at boot time
//   - baseTicks is the CNTVCT_EL0 value when the RTC was read
//   - frequency is the timer frequency in Hz
func GetBootEpoch() (baseSeconds, baseTicks, frequency uint64) {
	return state.baseSeconds, state.baseTicks, state.frequency
}

// GetFrequency returns the timer frequency in Hz.
func GetFrequency() uint64 {
	return state.frequency
}

// ReadCounter returns the current raw tick counter value.
func ReadCounter() uint64 {
	return kirq.ReadCounterValue()
}
