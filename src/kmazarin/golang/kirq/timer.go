//go:build qemuvirt && aarch64

package kirq

import "unsafe"

// getUartBase is provided by main package via go:linkname.
func getUartBase() uintptr

// getAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of runtime.asyncPreempt from RuntimeConfig,
// populated by Cardinal at boot time.
func getAsyncPreemptAddr() uintptr

// getReadyForAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of the readyForAsyncPreempt flag in kmazarin,
// which controls whether timer IRQs should trigger async preemption.
func getReadyForAsyncPreemptAddr() uintptr

// TimerIRQHandlerPreemptable handles the ARM Generic Timer interrupt (IRQ 27)
// Returns preemption info to trigger call injection if preemption should occur
//
//go:nosplit
//go:noinline
func TimerIRQHandlerPreemptable(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	// Re-arm timer for next interrupt (~100ms for responsive preemption)
	// Assuming 62.5MHz timer frequency
	// 100ms * 62.5MHz = 6250000 ticks = 0x5F5E10
	rearmTimer(0x5F5E10)

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

	// Check if runtime is ready for async preemption
	// This flag is set to false during runtime init, then true when main() starts
	readyFlagAddr := getReadyForAsyncPreemptAddr()
	if readyFlagAddr != 0 {
		// Read the flag value (uint32)
		readyFlag := *(*uint32)(unsafe.Pointer(readyFlagAddr))
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

	// Preemption enabled! Inject call to runtime.asyncPreempt
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
