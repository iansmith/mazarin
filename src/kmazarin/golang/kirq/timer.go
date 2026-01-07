//go:build qemuvirt && aarch64

package kirq

import (
	"unsafe"
)

// Address of runtime.asyncPreempt.abi0 in kmazarin
// This is where we inject calls for async preemption
// NOTE: This address may change with each build - verify with: nm build/kmazarin.elf | grep asyncPreempt
const asyncPreemptAddr = 0x41871b70

// TimerIRQHandlerPreemptable handles the ARM Generic Timer interrupt (IRQ 27)
// Returns preemption info to trigger call injection if preemption should occur
//
//go:nosplit
//go:noinline
func TimerIRQHandlerPreemptable(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) PreemptInfo {
	const uartBase = uintptr(0x09000000)
	*(*byte)(unsafe.Pointer(uartBase)) = 'T' // Debug: timer IRQ fired

	// Re-arm timer for next interrupt (~10ms)
	// Assuming 62.5MHz timer frequency
	// 10ms * 62.5MHz = 625000 ticks = 0x98968
	rearmTimer(0x98968)

	// Debug: show we're checking for preemption
	*(*byte)(unsafe.Pointer(uartBase)) = 'P'

	// For now, always trigger preemption when timer fires
	// TODO: Add logic to skip preemption in certain cases (e.g., if in scheduler)

	// Set up call injection:
	// 1. Allocate 16-byte frame on interrupted goroutine's stack
	// 2. Set ELR to asyncPreempt (where ERET will jump)
	// 3. Set LR to interrupted PC (so asyncPreempt can return)
	// 4. Adjust SP for the new frame

	adjustedSP := (spEl0 - 16) &^ 0xF // 16-byte aligned

	*(*byte)(unsafe.Pointer(uartBase)) = 'p' // Debug: preemption setup done

	return PreemptInfo{
		NewELR:    asyncPreemptAddr,
		NewSP:     adjustedSP,
		NewLR:     elr, // Interrupted PC
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
