//go:build arm64

package main

// MAZ-135 — woken-side gate for the priority-wake fast path (ARM64 side).
//
// ARM64 consumes priorityWakePending in exceptions_arm64.s and has a single g
// home (X28), so it has no dual-home `morestack on g0` hazard and needs no
// per-arch fast-switch helper here — only the shared gate predicate below.

// fastWakeAllowedForWaiter: ARM64 keeps fast-waking ALL waiters. The dual-home
// restore hazard that forbids P-released fast-resume on amd64 does not exist
// here (single-home g). Whether a P-handoff race could still affect a P-released
// waiter on ARM64 is an untested open question (it has never been observed);
// flipped to gate on pReleased if it ever is. MAZ-135.
//
//go:nosplit
func fastWakeAllowedForWaiter(pReleased bool) bool { return true }
