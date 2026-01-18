
// Package ktime provides kernel time services.
// The RTC is read once at initialization; subsequent time queries
// derive from the cached base time plus elapsed CPU ticks.
package ktime

import (
	"kmazarin/device"
	"kmazarin/kirq"
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
// Returns true on success, false if no clock device is available.
func Init() bool {
	if atomic.LoadUint32(&state.initialized) == 1 {
		return true
	}

	clock, ok := device.GetClock()
	if !ok {
		return false
	}

	// Read tick counter FIRST (minimize drift)
	ticks := kirq.ReadCounterValue()
	seconds, _ := clock.Now()
	freq := uint64(kirq.GetTimerFrequency())

	state.baseTicks = ticks
	state.baseSeconds = seconds
	state.frequency = freq
	atomic.StoreUint32(&state.initialized, 1)

	return true
}

// GetTime returns current time derived from RTC + elapsed ticks.
// Seconds is time since Unix epoch. Nanoseconds is the sub-second component.
// Init() must have been called before this function.
// Returns (0, 0) if not initialized.
//
//go:nosplit
func GetTime() (seconds, nanoseconds uint64) {
	if atomic.LoadUint32(&state.initialized) == 0 {
		return 0, 0
	}

	currentTicks := kirq.ReadCounterValue()
	elapsedTicks := currentTicks - state.baseTicks

	elapsedSeconds := elapsedTicks / state.frequency
	remainderTicks := elapsedTicks % state.frequency
	elapsedNanoseconds := (remainderTicks * 1_000_000_000) / state.frequency

	return state.baseSeconds + elapsedSeconds, elapsedNanoseconds
}
