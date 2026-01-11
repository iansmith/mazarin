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
