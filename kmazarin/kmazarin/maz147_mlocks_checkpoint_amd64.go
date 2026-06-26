//go:build amd64

package main

// MAZ-147 amd64-specific half: the preempt SKIP-guard `g0PreemptHoldsMLocks`. The
// arch-neutral save/globals/selftest are in maz147_mlocks_checkpoint.go; the amd64
// asm RESTORE is in exceptions_amd64.s at `load_context_and_iretq` (keyed on
// `ctx.R14 == kmazarinG0Addr`, re-arming via the precomputed `savedG0MLocksPtr`).

// g0PreemptHoldsMLocks reports whether an INVOLUNTARY timer preemption is about to
// switch g0 out while it holds m.locks — in which case checkThreadPreemptionImpl
// must SKIP the switch (return 0), mirroring stock Go's "m.locks ⇒ non-preemptible".
//
// Why skip (not checkpoint) on the preempt path: unlike the usleep yield (which
// MUST release the P → can't skip → R1 save/restore), timer preemption is
// involuntary, so simply NOT preempting g0 is exactly stock-Go semantics. g0 keeps
// running its (short) critical section, then either drops m.locks (preemptible
// again) or hits lock2 backoff → usleep (the voluntary yield R1 checkpoints). lock2
// active-spin is bounded, so this can't livelock. This is the CONFIRMED dominant
// leak path (PREEMPT-G0-LEAK ×6/9 boots; design §8c) — the timer was switching g0
// out holding m.locks with no save. A single guard here also covers
// boostThread0ForPendingWork (it runs later in checkThreadPreemptionImpl).
//
// Reads the LIVE interrupted g from the exception frame (not the stale
// oldThread.Context, which isn't refreshed until SaveContextFromFrame) via the
// shared kernelModeEffG helper — the SAME effective-g rule SaveContextFromFrame's
// kernel branch and gspUnsafeKernelResume use (gLooksValid(slot)?slot:R14). Only g0
// (the shared/borrowed system M) is protected — regular goroutines resume on their
// own M with m.locks intact (§4).
//
//go:nosplit
func g0PreemptHoldsMLocks(framePtr uintptr) bool {
	effG, ok := kernelModeEffG(framePtr)
	if !ok || effG != kmazarinG0Addr {
		return false // user-mode / pre-init, or not running g0 — its own M, not the borrowed m0
	}
	lp := g0MLocksPtr()
	if lp == nil {
		return false
	}
	return *lp > 0
}
