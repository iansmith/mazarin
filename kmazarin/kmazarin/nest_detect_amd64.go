//go:build amd64

package main

import "mazzy/kmazarin/klog"

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
var (
	excNestDepth    uint64 // live exception-nesting depth (== live IST chains)
	excNestMaxDepth uint64 // high-water mark of excNestDepth
	excNestCount    uint64 // entries that nested inside >=1 live chain
)

// dumpNestStats reports the nested-exception detector counters. Called from the
// shepherd-exit diagnostic dump (klog-safe, non-nosplit context). At exit
// excNestDepth should be ~0 (every entry returned); the durable signals are the
// cumulative nest count and the high-water depth.
func dumpNestStats() {
	klog.Criticalf("xmm-nest", "[xmm-nest] nested-entry detector: nests=%d maxdepth=%d depth=%d\n",
		excNestCount, excNestMaxDepth, excNestDepth)
}
