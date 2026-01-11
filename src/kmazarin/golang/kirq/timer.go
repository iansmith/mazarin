//go:build qemuvirt && aarch64

package kirq

import (
	"unsafe"
)

// getUartBase is provided by main package via go:linkname.
func getUartBase() uintptr

// getAsyncPreemptAddr is provided by main package via go:linkname.
// Returns the address of runtime.asyncPreempt from RuntimeConfig,
// populated by Cardinal at boot time.
func getAsyncPreemptAddr() uintptr

// TimerIRQHandlerPreemptable handles the ARM Generic Timer interrupt (IRQ 27)
// Returns preemption info to trigger call injection if preemption should occur
//
//go:nosplit
//go:noinline
func TimerIRQHandlerPreemptable(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	uartBase := getUartBase()
	*(*byte)(unsafe.Pointer(uartBase)) = 'T' // Debug: timer IRQ fired

	// DEBUG: Print ELR, X0, SP from frame to see thread state
	hexChars := "0123456789ABCDEF"

	// Read from frame - X0 at offset 0, SP_EL0 at offset 36
	frame := (*[40]uint64)(unsafe.Pointer(framePtr))
	x0 := frame[0]

	*(*byte)(unsafe.Pointer(uartBase)) = 'E'
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	for i := 28; i >= 0; i -= 4 {
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[(elr>>i)&0xF]
	}
	*(*byte)(unsafe.Pointer(uartBase)) = ' '
	*(*byte)(unsafe.Pointer(uartBase)) = 'X'
	*(*byte)(unsafe.Pointer(uartBase)) = '0'
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	for i := 28; i >= 0; i -= 4 {
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[(x0>>i)&0xF]
	}
	*(*byte)(unsafe.Pointer(uartBase)) = ' '
	*(*byte)(unsafe.Pointer(uartBase)) = 'S'
	*(*byte)(unsafe.Pointer(uartBase)) = 'P'
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	for i := 28; i >= 0; i -= 4 {
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[(spEl0>>i)&0xF]
	}
	*(*byte)(unsafe.Pointer(uartBase)) = '\r'
	*(*byte)(unsafe.Pointer(uartBase)) = '\n'

	// Re-arm timer for next interrupt (~1 second for debugging)
	// Assuming 62.5MHz timer frequency
	// 1s * 62.5MHz = 62500000 ticks = 0x3B9ACA0
	rearmTimer(0x3B9ACA0)

	// Get asyncPreempt address from RuntimeConfig (set by Cardinal at boot)
	asyncPreemptAddr := getAsyncPreemptAddr()

	// Check if preemption is enabled (address is non-zero)
	if asyncPreemptAddr == 0 {
		// Preemption disabled - address not configured
		*(*byte)(unsafe.Pointer(uartBase)) = 't' // Debug: timer acknowledged, no preempt
		return PreemptInfo{
			NewELR:    0,
			NewSP:     0,
			NewLR:     0,
			DoPreempt: false,
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
	*(*byte)(unsafe.Pointer(uartBase)) = 'P' // Debug: preempt triggered

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
