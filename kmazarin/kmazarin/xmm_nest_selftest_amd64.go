//go:build amd64

package main

import "mazzy/kmazarin/klog"

// MAZ-139 DoD#1 RED self-test — the XMM nested-exception clobber, exercised
// through the PRODUCTION exception path (NOT a standalone reimplementation; the
// prior probe was a tautology that always saved to two distinct slots and could
// never go RED).
//
// runXMMNestSelfTest loads sentinel A into XMM and takes a REAL outer exception
// (INT $48). While that outer entry is live, the one-shot hook in
// handle_timer_irq (exceptions_amd64.s) fires a REAL nested INT $48 with
// sentinel B, whose common_exception_entry overwrites the single global
// ·xmmSaveArea. The outer's exception_return then restores the clobbered global.
//
//   - RED  (single global): the outer resumes with sentinel B  → panic.
//   - GREEN (per-frame slot, item 2): the outer resumes with sentinel A → OK.
//
// Deterministic: the nesting is software-triggered (INT, IF-independent) and the
// caller masks IRQs with only thread 0 live, so no hardware IRQ interleaves.
// Gated by kernel.toml xmm_nest_test.
var (
	xmmNestSentA    [256]byte // outer sentinel — loaded into XMM before INT $48
	xmmNestSentB    [256]byte // nested sentinel — loaded by the timer-handler hook
	xmmNestGotOuter [256]byte // XMM read back after the outer exception_return
	xmmNestArmed    uint64    // one-shot: set here, cleared by the hook before the nested INT
)

// xmmNestProbe is implemented in xmm_nest_selftest_amd64.s.
//
//go:nosplit
func xmmNestProbe(sentA, gotOuter *byte)

func runXMMNestSelfTest() {
	for i := range xmmNestSentA {
		a := byte((i % 251) + 1) // 1..251, never 0
		xmmNestSentA[i] = a
		xmmNestSentB[i] = a ^ 0xFF // != a always; nonzero since a <= 251 (a^0xFF >= 4)
	}
	xmmNestGotOuter = [256]byte{}

	xmmNestArmed = 1 // the hook fires the nested INT $48 on this probe's outer entry
	xmmNestProbe(&xmmNestSentA[0], &xmmNestGotOuter[0])
	xmmNestArmed = 0 // defensive: ensure disarmed even if the hook never ran

	if xmmNestGotOuter == xmmNestSentA {
		klog.Criticalf("[XMM]", "[xmm-nest] OK — outer XMM survived a real nested exception (per-frame slot)\n")
		return
	}
	clobberedByNest := xmmNestGotOuter == xmmNestSentB
	klog.Errf("[xmm-nest] FAIL: outer XMM clobbered by a nested exception (==nested sentinel: %v) — ·xmmSaveArea is a single global, not per-exception-frame (MAZ-139 DoD#1)\n",
		clobberedByNest)
	panic("runXMMNestSelfTest: nested exception clobbered outer XMM")
}
