package kirq

import (
	_ "unsafe" // Required for //go:linkname directives
)

// Forward declarations for functions provided via go:linkname or assembly.
// These are excluded during test builds and replaced by stubs.

// processDeadlines is NO LONGER CALLED from timer IRQ context.
// Instead, the timer IRQ handler sets deadlinePending flag,
// and the deadline bottom half processor calls ProcessDeadlines
// in safe Go goroutine context.
//
//go:linkname processDeadlines main.ProcessDeadlines
func processDeadlines()

// Timer assembly functions have moved to the ktimer package.
// See kmazarin/ktimer/platform_arm64.go and platform_arm64.s.
