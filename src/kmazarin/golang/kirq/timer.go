//go:build qemuvirt && aarch64

package kirq

import (
	"unsafe"
)

// getUartBase is provided by main package via go:linkname.
func getUartBase() uintptr

// Address of runtime.asyncPreempt.abi0 in kmazarin
// This is where we inject calls for async preemption
// TODO: This hardcoded address is BROKEN and needs to be discovered dynamically!
// It changes with every build. We need to either:
// 1. Get it from Cardinal via auxv (add AT_ASYNC_PREEMPT)
// 2. Look it up via symbol table at runtime
// 3. Use a linker trick to get a reference
// For now, DISABLING PREEMPTION until we can do this properly.
const asyncPreemptAddr = 0 // DISABLED - see TODO above

// TimerIRQHandlerPreemptable handles the ARM Generic Timer interrupt (IRQ 27)
// Returns preemption info to trigger call injection if preemption should occur
//
//go:nosplit
//go:noinline
func TimerIRQHandlerPreemptable(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	uartBase := getUartBase()
	*(*byte)(unsafe.Pointer(uartBase)) = 'T' // Debug: timer IRQ fired

	// Re-arm timer for next interrupt (~10ms)
	// Assuming 62.5MHz timer frequency
	// 10ms * 62.5MHz = 625000 ticks = 0x98968
	rearmTimer(0x98968)

	// CRITICAL: Preemption is DISABLED because asyncPreemptAddr is hardcoded.
	// We need to fix this before enabling preemption.
	// For now, just acknowledge the timer and return without preempting.

	*(*byte)(unsafe.Pointer(uartBase)) = 't' // Debug: timer acknowledged, no preempt

	// Return without preemption
	return PreemptInfo{
		NewELR:    0,
		NewSP:     0,
		NewLR:     0,
		DoPreempt: false, // DISABLED until asyncPreemptAddr is fixed
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
