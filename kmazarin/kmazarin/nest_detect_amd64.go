//go:build amd64

package main

import (
	"sync/atomic"

	"mazzy/kmazarin/klog"
)

// MAZ-139 — hot-path nested-exception detector (amd64).
//
// These counters mirror the MAZ-136 IST cursor's live-level accounting: the +1
// at common_exception_entry (next to istSubCount) and the two -1s at the cursor
// ADDs in exception_return / load_context_and_iretq. So excNestDepth is the
// number of LIVE exception chains at any instant, and excNestCount counts how
// often a second exception entry nests inside a live chain — the precondition
// for the single-global exception save-state clobber (xmmSaveArea et al.) that
// this ticket fixes.
//
// Phase D1 (measure-first): on the still-global code, this measures the natural
// frequency of clobber-prone nesting — the number that does not exist today.
// Once XMM moves into the exception frame (item 2), this upgrades to D2: a
// per-frame canary that asserts the OUTER level's slot survived a nested entry,
// turning the detector into a kept low-cost guard that proves the per-frame fix
// is live and protecting (and alarms if a clobber is ever observed).
//
// Storage is owned here (Go vars), referenced from exceptions_amd64.s as
// ·excNestDepth(SB) etc. — the same Go-var / no-asm-GLOBL pattern as
// istSubCount (syscall_amd64.go).
//
// SMP-safe / lock-free: the asm mutates these with LOCK-prefixed atomics — LOCK
// XADD to bump the depth (and read its old value), LOCK DEC on the two retires,
// LOCK INC on the cumulative counters, and a LOCK CMPXCHG CAS loop for the
// high-water max — and dumpNestStats reads them with atomic loads. No lost updates
// or torn reads across CPUs. The cumulative counters (excNestCount, excCanary*)
// aggregate correctly across all CPUs; excNestDepth / excNestMaxDepth become an
// aggregate (the live sum across CPUs / its high-water) rather than per-CPU values —
// faithful per-CPU nesting state is part of x86 SMP (MAZ-142).
var (
	excNestDepth    uint64 // live exception-nesting depth (== live IST chains)
	excNestMaxDepth uint64 // high-water mark of excNestDepth
	excNestCount    uint64 // entries that nested inside >=1 live chain
)

// D2 canary accounting (MAZ-139 DoD#2). common_exception_entry stamps each
// level's per-frame slot with SP^excFrameCanaryMagic; exception_return verifies
// it. excCanaryCheckCount counts intact verifications; excCanaryAlarmCount counts
// mismatches. The inference contract: nests>0 AND checks>0 AND alarms==0 ⇒ the
// per-frame fix is LIVE and PROTECTING (every nested entry left outer slots
// intact). alarms>0 ⇒ a nested level reached an outer slot — the fix is not live
// or has regressed (guards the [[feedback_runtime_overlay_verify]] "stock runtime
// shipped" failure mode). Silence proves nothing. Referenced from
// exceptions_amd64.s as ·excCanary*(SB).
var (
	excCanaryCheckCount uint64 // exception_return canary verifications found intact
	excCanaryAlarmCount uint64 // canary mismatches (per-frame slot reached by another level) — should be 0
)

// dumpNestStats reports the nested-exception detector counters. Called from the
// shepherd-exit diagnostic dump (klog-safe, non-nosplit context). At exit
// excNestDepth should be ~0 (every entry returned); the durable signals are the
// cumulative nest count and the high-water depth.
func dumpNestStats() {
	klog.Criticalf("xmm-nest", "[xmm-nest] nested-entry detector: nests=%d maxdepth=%d depth=%d | D2 canary: checks=%d alarms=%d\n",
		atomic.LoadUint64(&excNestCount), atomic.LoadUint64(&excNestMaxDepth), atomic.LoadUint64(&excNestDepth),
		atomic.LoadUint64(&excCanaryCheckCount), atomic.LoadUint64(&excCanaryAlarmCount))
}
