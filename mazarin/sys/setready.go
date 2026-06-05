package sys

import (
	"time"

	"mazzy/shared/mazzy"
)

// shepherdStart / shepherdName are recorded by MarkShepherdStart so SetReady can
// report time-to-ready. Per-process state (each shepherd is its own process),
// so no cross-shepherd contamination.
var (
	shepherdStart time.Time
	shepherdName  string
)

// MarkShepherdStart records this shepherd's name and start time so SetReady can
// log how long the shepherd took to become ready. Call once at the very top of
// the shepherd's entry point (main / MazarinMain). Works for both ELF and .maz
// shepherds.
func MarkShepherdStart(name string) {
	shepherdName = name
	shepherdStart = time.Now()
}

// SetReady signals to the kernel that this shepherd is ready to accept
// delegated work (e.g., syscall handling). Must be called after the
// shepherd has completed initialization and registered its handlers.
func SetReady(ready bool) {
	var val uintptr
	if ready {
		val = 1
	}
	RawSyscall(mazzy.SysSetReady, val, 0, 0, 0, 0, 0)
	// Report time-from-start-to-ready for boot-perf diagnosis. Slow cloud hosts
	// (nested KVM + network disk) make shepherds block in fs reads loading their
	// .maz, so this number is the key signal for tuning fs's readiness budget.
	if ready && !shepherdStart.IsZero() {
		ms := int64(time.Since(shepherdStart) / time.Millisecond)
		UartWriteString("[" + shepherdName + "] ready in " + Itoa(ms) + "ms\n")
	}
}
