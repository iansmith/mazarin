//go:build test_stubs

package ktimer

// Test stubs for platform timer functions.
// Used when building with -tags test_stubs to allow unit testing
// without real hardware.

func PlatformTimerInit() uint32    { return 0 }
func PlatformReadCounter() uint64  { return 0 }
func PlatformRearmTimer(ticks uint64) {}
func PlatformTimerIRQ() uint32     { return 0 }
