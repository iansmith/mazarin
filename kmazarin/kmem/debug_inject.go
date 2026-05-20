package kmem

import "sync/atomic"

// MapPageInProcessFailNext arms a one-shot failure: when true, the next call
// to MapPageInProcess will clear the flag and return false without doing any
// work.
//
// One-shot rather than a counter because every rollback path also calls
// MapPageInProcess to restore the caller's mapping — a counter > 1 would
// inject failures into the rollback itself and trip KernelPanic. The only
// safe arming is "fire the next forward call, then disarm."
//
// Used by xfertest's TransferDMAClump rollback stage to provoke the
// mid-transfer failure path. Production cost: one atomic.Load + branch per
// MapPageInProcess call; default-false → predicted-never-taken.
//
// Set via SyscallDebugPrint marker DebugMarkerSetMapFailNext (0xDB8).
var MapPageInProcessFailNext atomic.Bool

// MapPageInProcessFailAfter is a cross-chunk-capable failure-injection
// counter, sibling to the one-shot MapPageInProcessFailNext above. It
// lets a test fail the Nth forward call after arming rather than the
// very next one — required for MAZ-37, where the rollback path must be
// exercised mid-clump, not on the first chunk.
//
// Convention (chosen for race-freedom and ease-of-reasoning):
//
//   - Default value is 0, meaning "disarmed". Any value <= 0 is disarmed.
//   - To arm "fail call N from now (1-indexed)", store N (N >= 1).
//     For example, SetMapFailAfter(1) fires on the very next forward
//     call (equivalent to but independent of the one-shot bool above);
//     SetMapFailAfter(4) fires on the 4th forward call after arming
//     (three calls decrement 4→3→2→1; the 4th call's Add(-1) returns
//     0, fires, and leaves the counter at 0 = "disarmed").
//   - Each MapPageInProcess forward call does
//     n := MapPageInProcessFailAfter.Add(-1); if n == 0, that call
//     fires. Subsequent decrements yield -1, -2, … all < 0 (still
//     "disarmed" by the <= 0 rule), so rollback paths calling
//     MapPageInProcess will NOT re-fire — fire-and-disarm semantics
//     are inherent to the convention, no extra CAS needed.
//   - SetMapFailAfter(0) (or any non-positive value) is a no-op arm,
//     i.e. an explicit disarm — the next Add(-1) returns -1, ≠ 0,
//     so nothing fires.
//
// Production cost: one atomic.Add + branch per MapPageInProcess call;
// default-0 → predicted-never-taken since 0 + (-1) = -1 ≠ 0.
//
// Set via SyscallDebugPrint marker DebugMarkerSetMapFailAfter (0xDB9).
var MapPageInProcessFailAfter atomic.Int32

// ConsumeMapFailInjection returns true (and disarms, if applicable) if
// EITHER injection knob fires on this call; otherwise returns false.
// Checks the one-shot bool first (cheaper CAS), then the countdown
// counter — both are independent so either can fire on any given call.
//
// Convention for the counter knob: each call decrements via Add(-1) and
// fires when the post-decrement value equals 0 (i.e. the pre-decrement
// value was 1). Values < 0 are also "disarmed" since further decrements
// just push further negative; this is the fire-and-disarm guarantee that
// keeps rollback paths from re-firing. See MapPageInProcessFailAfter for
// the full convention and SetMapFailAfter arming semantics.
//
// Nosplit so it can be called from inside transferDMAClumpInner's
// IRQ-disabled critical section. All operations are atomic-only, which
// is nosplit-safe by construction.
//
//go:nosplit
func ConsumeMapFailInjection() bool {
	if MapPageInProcessFailNext.CompareAndSwap(true, false) {
		return true
	}
	if MapPageInProcessFailAfter.Add(-1) == 0 {
		return true
	}
	return false
}
