package kirq

import (
	"mazzy/kmazarin/ktimer"
	"sync/atomic"
	"unsafe"
)

// InitTimer initializes the hardware timer via ktimer.
// Kept as a thin wrapper for backward compatibility with existing init() calls.
//
//go:nosplit
func InitTimer() {
	ktimer.Init()
}

// TimerIRQHandlerCanPreempt handles the timer interrupt.
// Returns preemption info to trigger call injection if goroutine preemption should occur.
//
// NOTE: This Go handler is not currently called for timer IRQs - the assembly
// handler (TimerIRQHandlerAsm) is used instead for safety in IRQ context.
// This remains here for future use if we need Go-level timer handling.
//
//go:nosplit
//go:noinline
func TimerIRQHandlerCanPreempt(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	// Re-arm timer for next interrupt (~100ms for responsive preemption)
	// 100ms * frequency = ticks
	ticks := (uint64(ktimer.Frequency()) * 100) / 1000
	ktimer.Rearm(ticks)

	// NOTE: Deadline processing now happens in bottom half (safe Go context)
	// The assembly timer handler sets deadlinePending flag instead of calling
	// processDeadlines() directly from IRQ context.

	// Suppress unused warnings
	_ = irqNum
	_ = framePtr

	// Get asyncPreempt address from RuntimeConfig (set by Cardinal at boot)
	asyncPreemptAddr := getAsyncPreemptAddr()

	// Check if preemption is enabled (address is non-zero)
	if asyncPreemptAddr == 0 {
		return PreemptInfo{}
	}

	// CRITICAL: Don't preempt if we're already executing asyncPreempt
	// This prevents nested preemptions which could corrupt the stack
	// asyncPreempt is ~100 bytes, so check if ELR is within [asyncPreempt, asyncPreempt+256)
	if elr >= uint64(asyncPreemptAddr) && elr < uint64(asyncPreemptAddr)+256 {
		return PreemptInfo{}
	}

	// CRITICAL: Validate that ELR is properly 4-byte aligned
	// ARM64 instructions must be 4-byte aligned. If ELR is misaligned,
	// it indicates corruption and we should not attempt preemption.
	if (elr & 0x3) != 0 {
		return PreemptInfo{}
	}

	// CRITICAL: Validate that asyncPreempt address is properly 4-byte aligned
	if (uint64(asyncPreemptAddr) & 0x3) != 0 {
		return PreemptInfo{}
	}

	// Check if runtime is ready for async preemption
	// This flag is set to false during runtime init, then true when main() starts
	readyFlagAddr := getReadyForAsyncPreemptAddr()
	if readyFlagAddr != 0 {
		// Read the flag value atomically (accessed from both main and IRQ context)
		readyFlag := atomic.LoadUint32((*uint32)(unsafe.Pointer(readyFlagAddr)))
		if readyFlag == 0 {
			return PreemptInfo{}
		}
	}

	// Check if assembly handler requested async preemption
	// NeedsAsyncPreempt is set by TimerIRQHandlerAsm when the same goroutine
	// has been running for too long (threshold exceeded)
	//
	// For SMP safety: Assembly writes to the global, we copy it to per-CPU here.
	// This ensures each CPU sees its own preemption state.
	needsPreempt := atomic.LoadUint32(&NeedsAsyncPreempt)
	syncGlobalNeedsAsyncPreemptToPerCPU(needsPreempt)

	if needsPreempt == 0 {
		return PreemptInfo{}
	}

	// Clear the flag now that we've read it (both global and per-CPU)
	atomic.StoreUint32(&NeedsAsyncPreempt, 0)
	setPerCPUNeedsAsyncPreempt(0)

	// Preemption needed! Inject call to runtime.asyncPreempt
	// 1. NewELR = asyncPreemptAddr (where to "return" to)
	// 2. NewLR = original ELR (so asyncPreempt returns to original code)
	// 3. SP unchanged
	return PreemptInfo{
		NewELR:    uint64(asyncPreemptAddr),
		NewSP:     spEl0,
		NewLR:     elr,
		DoPreempt: true,
	}
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
