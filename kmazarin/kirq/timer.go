package kirq

import (
	"mazzy/kmazarin/ktimer"
	"sync/atomic"
)

// InitTimer initializes the hardware timer via ktimer.
// Kept as a thin wrapper for backward compatibility with existing init() calls.
//
//go:nosplit
func InitTimer() {
	ktimer.Init()
}

// TimerIRQHandlerCanPreempt handles the timer interrupt from the dispatch table.
// The actual timer handling and thread preemption is done by TimerIRQHandlerAsm
// (pure assembly). This Go handler just re-arms the timer as a fallback.
//
// Goroutine-level preemption is now handled by the Go runtime in userspace
// via SIGURG signals — the kernel no longer injects asyncPreempt.
//
//go:nosplit
//go:noinline
func TimerIRQHandlerCanPreempt(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	// Increment timer IRQ counter (on ARM64/RISC-V this is done by
	// TimerIRQHandlerAsm in assembly; on x86_64 we do it here since
	// the pure-asm handler is not in the interrupt path).
	c := atomic.AddUint64(&TimerIRQCount, 1)

	// Track counter values for Hz calculation
	now := ktimer.ReadCounter()
	if c == 1 {
		atomic.StoreUint64(&DbgTimerFirstCounter, now)
	}
	atomic.StoreUint64(&DbgTimerLatestCounter, now)

	// Re-arm timer for next kernel tick (TickIntervalMs, derived in InitPreemptThresholds)
	ktimer.Rearm(TimerRearmTicks)

	// Suppress unused warnings
	_ = irqNum
	_ = framePtr
	_ = elr
	_ = spEl0

	return PreemptInfo{}
}

// GetTimerFrequency returns the cached timer frequency in Hz.
// Thin wrapper around ktimer.Frequency() for backward compatibility.
//
//go:nosplit
func GetTimerFrequency() uint32 {
	return ktimer.Frequency()
}

// ReadCounterValue reads the current timer counter value.
// Thin wrapper around ktimer.ReadCounter() for backward compatibility.
//
//go:nosplit
func ReadCounterValue() uint64 {
	return ktimer.ReadCounter()
}
