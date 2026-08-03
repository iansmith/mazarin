
#include "textflag.h"

// OVERVIEW: CPU idle instruction for power-efficient waiting
//
// WaitForInterrupt executes the ARM64 WFI (Wait For Interrupt) instruction,
// putting the CPU into a low-power idle state until an interrupt arrives.
// This is used by the idle loop when all threads are blocked, reducing
// CPU usage while waiting for timer interrupts to process deadlines.
//
// The WFI instruction:
// - Puts the processor into a low-power standby state
// - Wakes immediately when an interrupt becomes pending
// - Is a hint instruction - the processor may also wake spuriously
//
// Under QEMU/HVF the guest core never actually halts: Hypervisor.framework
// always intercepts WFI, and QEMU's handler sleeps the vCPU *host thread* until
// the next armed CNTV deadline (with a ~2 ms floor below which it returns
// immediately). The parking is real, it just happens host-side. YIELD, which
// this used to encode by mistake, does not trap at all — which is why the idle
// loop used to peg a host core (MAZ-169).

// func WaitForInterrupt()
TEXT ·WaitForInterrupt(SB), NOSPLIT|NOFRAME, $0-0
	WFI
	RET

// EnableIRQsAndWait enables IRQs and halts until the next interrupt.
// MSR DAIFClr, #2 clears the I bit to enable IRQs, then WFI halts.
// Exception return restores the caller's original DAIF state.
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	MSR	$2, DAIFClr	// enable IRQs
	ISB	$15
	WFI
	// DMB OSH — data memory barrier (outer shareable) after WFI wake.
	// Under HVF, the VirtIO backend runs on a separate host thread. Its DMA
	// writes (used ring) may not be visible to this vCPU without a barrier
	// after the interrupt handler returns via ERET. ERET is a context sync
	// event (ISB-like) but does NOT imply a data barrier. This DMB ensures
	// the device's DMA writes are ordered before any subsequent loads
	// (e.g., reading the used ring in DoBlockIOComplete).
	// On TCG this is harmless (single-threaded, trivially consistent).
	DMB	$3
	MSR	$2, DAIFSet	// re-disable IRQs
	RET
