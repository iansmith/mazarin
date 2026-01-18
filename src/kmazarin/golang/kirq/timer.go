
package kirq

import (
	"sync/atomic"
	"unsafe"
)

// getAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of runtime.asyncPreempt from RuntimeConfig,
// populated by Cardinal at boot time.
func getAsyncPreemptAddr() uintptr

// getReadyForAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of the readyForAsyncPreempt flag in kmazarin,
// which controls whether timer IRQs should trigger async preemption.
func getReadyForAsyncPreemptAddr() uintptr

// processDeadlines is NO LONGER CALLED from timer IRQ context.
// Instead, the timer IRQ handler sets deadlinePending flag,
// and the deadline bottom half processor calls ProcessDeadlines
// in safe Go goroutine context.
//
// This linkname is kept for reference but is no longer used.
//
//go:linkname processDeadlines main.ProcessDeadlines
func processDeadlines()

// timerFrequency caches the counter frequency (read once at initialization)
// Default to 62.5MHz for safety if not initialized
var timerFrequency uint32 = 62500000

// InitTimer reads the counter frequency from CNTFRQ_EL0 and caches it
// Should be called during early initialization
//
//go:nosplit
func InitTimer() {
	freq := asm_readCntfrqEl0()
	if freq > 0 {
		timerFrequency = freq
	}
}

// TimerIRQHandlerCanPreempt handles the ARM Generic Timer interrupt (IRQ 27)
// Returns preemption info to trigger call injection if goroutine preemption should occur
//
// NOTE: This Go handler is not currently called for timer IRQs - the assembly
// handler (TimerIRQHandlerAsm) is used instead for safety in IRQ context.
// This remains here for future use if we need Go-level timer handling.
//
//go:nosplit
//go:noinline
func TimerIRQHandlerCanPreempt(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	// Re-arm timer for next interrupt (~100ms for responsive preemption)
	// Calculate ticks based on actual timer frequency (read from CNTFRQ_EL0)
	// 100ms * frequency = ticks
	ticks := (uint64(timerFrequency) * 100) / 1000

	rearmTimer(int32(ticks))

	// NOTE: Deadline processing now happens in bottom half (safe Go context)
	// The assembly timer handler sets deadlinePending flag instead of calling
	// processDeadlines() directly from IRQ context.
	//
	// OLD (UNSAFE): processDeadlines()  // Called Go code from IRQ context!
	// NEW (SAFE): Timer handler sets flag → bottom half goroutine processes

	// Suppress unused warnings
	_ = irqNum
	_ = framePtr

	// Get asyncPreempt address from RuntimeConfig (set by Cardinal at boot)
	asyncPreemptAddr := getAsyncPreemptAddr()

	// Check if preemption is enabled (address is non-zero)
	if asyncPreemptAddr == 0 {
		// Preemption disabled - address not configured
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
		}
	}

	// CRITICAL: Don't preempt if we're already executing asyncPreempt
	// This prevents nested preemptions which could corrupt the stack
	// asyncPreempt is ~100 bytes, so check if ELR is within [asyncPreempt, asyncPreempt+256)
	if elr >= uint64(asyncPreemptAddr) && elr < uint64(asyncPreemptAddr)+256 {
		// We're currently executing inside asyncPreempt - skip preemption
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
		}
	}

	// CRITICAL: Validate that ELR is properly 4-byte aligned
	// ARM64 instructions must be 4-byte aligned. If ELR is misaligned,
	// it indicates corruption and we should not attempt preemption.
	if (elr & 0x3) != 0 {
		// ELR is misaligned - skip preemption to avoid PC alignment fault
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
		}
	}

	// CRITICAL: Validate that asyncPreempt address is properly 4-byte aligned
	if (uint64(asyncPreemptAddr) & 0x3) != 0 {
		// asyncPreempt is misaligned - should never happen!
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
		}
	}

	// Check if runtime is ready for async preemption
	// This flag is set to false during runtime init, then true when main() starts
	readyFlagAddr := getReadyForAsyncPreemptAddr()
	if readyFlagAddr != 0 {
		// Read the flag value atomically (accessed from both main and IRQ context)
		readyFlag := atomic.LoadUint32((*uint32)(unsafe.Pointer(readyFlagAddr)))
		if readyFlag == 0 {
			// Runtime not ready - skip preemption
			return PreemptInfo{
				NewELR:    0,
				NewSP:     0,
				NewLR:     0,
				DoPreempt: false,
			}
		}
	}

	// Check if assembly handler requested async preemption
	// NeedsAsyncPreempt is set by TimerIRQHandlerAsm when the same goroutine
	// has been running for too long (threshold exceeded)
	needsPreempt := atomic.LoadUint32(&NeedsAsyncPreempt)
	if needsPreempt == 0 {
		// Threshold not exceeded - skip async preemption
		// (cooperative preemption via g.stackguard0 is still active)
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
		}
	}

	// Clear the flag now that we've read it
	atomic.StoreUint32(&NeedsAsyncPreempt, 0)

	// Preemption needed! Inject call to runtime.asyncPreempt
	// This will cause the interrupted code to call asyncPreempt when it returns
	// from the exception handler.
	//
	// We do this by:
	// 1. Setting NewELR to asyncPreemptAddr (where to "return" to)
	// 2. Setting NewLR to original ELR (so asyncPreempt returns to original code)
	// 3. Keeping SP unchanged
	return PreemptInfo{
		NewELR:    uint64(asyncPreemptAddr), // Jump to asyncPreempt
		NewSP:     spEl0,                    // Keep current stack
		NewLR:     elr,                      // Return to interrupted instruction
		DoPreempt: true,
	}
}

// rearmTimer sets the timer to fire after 'ticks' clock cycles
//
//go:nosplit
func rearmTimer(ticks int32) {
	// MSR CNTV_TVAL_EL0, X0
	// Encoded as: 0xD51BE000 | (Xt << 0)
	// For R0: 0xD51BE000
	var val int32 = ticks

	// Assembly: MSR CNTV_TVAL_EL0, R0
	asm_rearmTimer(uint64(val))
}

// asm_rearmTimer is implemented in timer_arm64.s
func asm_rearmTimer(ticks uint64)

// asm_readCntfrqEl0 is implemented in timer_arm64.s
// Reads CNTFRQ_EL0 (Counter Frequency Register) and returns the timer frequency in Hz
func asm_readCntfrqEl0() uint32

// asm_readCntvctEl0 is implemented in timer_arm64.s
// Reads CNTVCT_EL0 (Counter Value Register) and returns the current counter value
func asm_readCntvctEl0() uint64

// GetTimerFrequency returns the cached timer frequency in Hz
// Used by syscall handlers that need to convert counter values to time
//
//go:nosplit
func GetTimerFrequency() uint32 {
	return timerFrequency
}

// ReadCounterValue reads the current timer counter value
// Returns the raw counter value from CNTVCT_EL0
//
//go:nosplit
func ReadCounterValue() uint64 {
	return asm_readCntvctEl0()
}
