
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

// EnableIRQsAndWait halts until the next interrupt, then returns with IRQs
// masked — unconditionally, whatever the caller's entry state was.
//
// WFI comes FIRST, while IRQs are still masked. ARM64 treats a pending physical
// interrupt as a WFI wake-up event regardless of PSTATE.I, so parking before
// unmasking cannot lose a wake. Unmasking first would: ARM64 has no equivalent
// of x86's STI shadow (which wfi_amd64.s relies on to make STI+HLT atomic), so
// an interrupt taken between DAIFClr and WFI is handled and EOI'd before the
// WFI executes, and the core then parks with nothing left pending. That window
// was harmless while this encoded YIELD, which never parks; making it a real
// WFI is what made it live (MAZ-169 review).
//
// func EnableIRQsAndWait()
TEXT ·EnableIRQsAndWait(SB), NOSPLIT|NOFRAME, $0-0
	WFI
	MSR	$2, DAIFClr	// now let the pending interrupt be taken
	ISB	$15
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
