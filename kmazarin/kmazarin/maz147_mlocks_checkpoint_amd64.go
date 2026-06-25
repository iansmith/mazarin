//go:build amd64

package main

import (
	"unsafe"

	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
)

// MAZ-147 — Option B: checkpoint runtime `m.locks` across the kernel's raw
// context switch so a leaked count on the borrowed g0/m0 is never observed by
// foreign code that reuses or borrows m0.
//
// Root cause (design/maz147-mlocks-leak-root-cause.md): the Go runtime's `lock2`
// mutex backoff runs `runtime.usleep` on g0 with `m.locks` nested; kmazarin's
// `nanosleep` handler raw-switches g0 (`doContextSwitchImpl`) while it holds
// `m.locks`, leaking the count onto m0. When a borrowed-m0 syscall handler or a P
// returning to m0 reuses it before g0 resumes-and-unlocks, the next
// `runtime.schedule()` sees `m.locks != 0` → `fatal error: schedule: holding
// locks`. The fatal element is the *foreign visibility* of the leaked count, NOT
// the switch itself (the benign heap-M lock2 spinners switch with locks held
// ~2245×/boot and survive, because a regular goroutine resumes on its own M).
//
// Fix: when g0 is switched OUT carrying `m.locks > 0`, stash the count and zero
// the live `m.locks` (m0 then presents 0 to any foreign reuse); when g0 is
// switched back IN, restore it. Zeroing is safe because the *actual* runtime
// lock g0 holds stays locked in memory (contenders still block correctly) — only
// the non-preemption *hint* is hidden for the foreign window, which foreign code
// does not need.
//
// SAVE/RESTORE SPLIT — resolved by the OPEN-#1 audit (design doc §8): kmazarin has
// TWO independent switch funnels — `doContextSwitchImpl` (directed/syscall
// switches) and `checkThreadPreemptionImpl` (timer preemption / priority-wake /
// idle pickup) — that converge only at the assembly resume chokepoint
// `load_context_and_iretq`. g0's usleep→nanosleep is SAVED here (this file, called
// from `doContextSwitchImpl`), but g0's thread is marked sleeping, woken by the
// timer, and RESUMED by the preemption funnel — which never returns through
// `doContextSwitchImpl`. So the RESTORE must live at the chokepoint, NOT at any
// single Go funnel; it is implemented in `exceptions_amd64.s:load_context_and_iretq`
// (right beside the MAZ-135 per-thread TLS-g restore), keyed on
// `ctx.TLSG == kmazarinG0Addr`, re-arming `m.locks` via the precomputed
// `savedG0MLocksPtr` below. Restoring at only `doContextSwitchImpl` (the first cut)
// missed the dominant resume path and lost the count.
//
// AMD64-ONLY FOR NOW (flagged divergence, not permanent): validation is
// amd64-first; the ARM64 save+restore pair lands together when ARM64 validation
// begins (design doc §3 step 3). ⚠ Do NOT ship an ARM64 save without its ARM64
// asm restore — that would zero `g0.m.locks` and never re-arm it. The arm64 build
// gets a no-op `mlockCheckpointSave` stub (maz147_mlocks_checkpoint_arm64.go) so
// the shared `doContextSwitchImpl` call site stays arch-portable while ARM64
// `g0.m.locks` is left UNTOUCHED.
//
// SCOPE: keyed on the outgoing context's `g == kmazarinG0Addr` (the always-mapped
// kernel g0), so we only ever dereference g0 — never an arbitrary/sentinel g (the
// GSPMM #PF lesson). The evidence shows only g0 leaks (the crash signature is
// always g0/m0); regular goroutines don't, so they need no checkpoint.
//
// SINGLE GLOBAL SLOT: one g0 ⇒ one checkpoint. g0 cannot be switched out twice
// while already out (between save and resume g0 is not running), so the stash is
// always consumed by the very next g0 load before a new save can occur. A
// borrowed-m0 syscall ENTRY is not a `load_context_and_iretq` resume of g0, so the
// borrow correctly observes the zeroed count without touching the slot.
//
// SMP: single global slot, CPU-0/m0-scoped (matches the MAZ-143 amd64 CPU-0 scope
// → MAZ-142 for full SMP). Save runs under schedulerLock+IRQs-off; the asm restore
// runs IRQ-off before iretq — no concurrent access on a single CPU.

var (
	// savedG0MLocks is g0's m.locks while it is switched out (0 = nothing
	// checkpointed). The asm restore at load_context_and_iretq consumes it
	// (re-arms g0.m.locks and clears this back to 0).
	savedG0MLocks int32
	// savedG0MLocksPtr is &g0.m.locks, precomputed in Go at save time so the
	// nosplit/no-frame asm restore can re-arm the count WITHOUT recomputing the
	// cross-package kirq g→m / m→locks offsets in assembly. g0.m (== m0) is
	// stable, so the pointer stays valid between the save and the matching resume.
	savedG0MLocksPtr uintptr
)

// g0MLocksPtr returns a pointer to g0.m.locks, or nil if g0/the offsets aren't
// established yet (early boot). Derefs only kmazarinG0Addr (always mapped).
//
//go:nosplit
func g0MLocksPtr() *int32 {
	g0 := kmazarinG0Addr
	if g0 == 0 {
		return nil
	}
	gmOff := kirq.PreemptGMOffset
	mlOff := kirq.PreemptMLocksOffset
	if gmOff == 0 || mlOff == 0 {
		return nil // offsets not initialised yet (both are set as a unit; bail if either is unset)
	}
	m := *(*uintptr)(unsafe.Pointer(uintptr(g0) + gmOff))
	if m == 0 {
		return nil
	}
	return (*int32)(unsafe.Pointer(m + mlOff))
}

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

// mlockCheckpointSave is called in doContextSwitchImpl when g0 is being switched
// OUT to a real next thread (past the newThread==nil guard). If the outgoing
// context is g0 carrying m.locks>0, stash the count + its address and present 0
// on m0 for the foreign window. The matching re-arm happens at the
// load_context_and_iretq resume chokepoint (see file header).
//
//go:nosplit
func mlockCheckpointSave(outgoing *Thread) {
	if outgoing == nil {
		return
	}
	g0 := kmazarinG0Addr
	if g0 == 0 || outgoing.Context.GetGRegister() != g0 {
		return // only the borrowed g0 leaks; regular goroutines resume on their own M
	}
	lp := g0MLocksPtr()
	if lp == nil {
		return
	}
	if *lp > 0 {
		savedG0MLocks = *lp
		savedG0MLocksPtr = uintptr(unsafe.Pointer(lp))
		*lp = 0
	}
}

// mlockTestThread is a package-global synthetic Thread for the selftest below —
// kept off the stack so its address never crosses a morestack-capable call (the
// MAZ-139 selftest-soundness lesson: a stack-backed object whose address the test
// threads through splittable calls can move under it).
var mlockTestThread Thread

// runMLockCheckpointSelfTest is the deterministic RED→GREEN for MAZ-147 Option B.
// It proves the SAVE-side invariant — the half that is RED without the fix and
// GREEN with it: when g0 is switched out holding m.locks, a FOREIGN reader of
// g0.m.locks sees 0 (so a borrowed/reused m0 never trips schedule:holding-locks),
// while the count AND its address are stashed for the resume-time re-arm. It also
// proves the GATE (a non-g0 outgoing context is never checkpointed) and the
// re-arm DATA CONTRACT (writing savedG0MLocks through savedG0MLocksPtr restores
// the exact count to g0.m.locks).
//
// What this does NOT cover: the literal asm restore instructions at
// load_context_and_iretq — a context load can't be unit-invoked (it iretq's away).
// Those are covered by the objdump verification + the TCG batch (THE gate), where
// real g0 nanosleep→resume cycles exercise the asm and assert no
// schedule:holding-locks and leak-free G0-LEAK. This test pins the Go contract the
// asm consumes (correct pointer, correct value, correct gate).
//
// Shares the gated, quiescent, thread-0-only boot point + IRQ masking with the
// other ctx_marshal_test selftests. The real g0.m.locks is borrowed for a tiny
// IRQ-masked window with NO allocation/klog inside, and fully restored (along with
// the checkpoint globals) before the assertions, which klog/panic (and allocate).
func runMLockCheckpointSelfTest() {
	g0 := kmazarinG0Addr
	if g0 == 0 {
		klog.Errf("[mlock-selftest] FAIL: kmazarinG0Addr not set at selftest time\n")
		panic("runMLockCheckpointSelfTest: kmazarinG0Addr not set")
	}
	lp := g0MLocksPtr()
	if lp == nil {
		klog.Errf("[mlock-selftest] FAIL: g0MLocksPtr nil (g→m/m→locks offsets not established)\n")
		panic("runMLockCheckpointSelfTest: g0MLocksPtr nil")
	}

	const testLocks int32 = 3 // the exact crash signature (m.locks=3)

	// Results captured inside the masked window; asserted afterwards.
	var foreignSaw int32 // what a foreign reader sees while g0 is "switched out"
	var stashCount int32 // savedG0MLocks after the save
	var stashPtrOK bool  // savedG0MLocksPtr == &g0.m.locks
	var rearmed int32    // g0.m.locks after the re-arm
	var slotCleared bool // savedG0MLocks cleared by the re-arm
	var gateHeld bool    // a non-g0 outgoing left the checkpoint untouched

	daif := SaveAndDisableIRQs()
	orig := *lp                       // real g0.m.locks (0 at this quiescent boot point)
	saveSlot := savedG0MLocks         // preserve any live checkpoint (there should be none)
	savePtr := savedG0MLocksPtr       // ...and its pointer
	wantPtr := uintptr(unsafe.Pointer(lp))

	// GATE: a non-g0 outgoing context must NOT be checkpointed.
	savedG0MLocks = 0
	*lp = testLocks
	mlockTestThread.Context.SetGRegister(g0 + 0x1000) // any non-g0 g
	mlockCheckpointSave(&mlockTestThread)
	gateHeld = savedG0MLocks == 0 && *lp == testLocks

	// SAVE: g0 outgoing holding m.locks=3 → foreign sees 0, count+addr stashed.
	savedG0MLocks = 0
	savedG0MLocksPtr = 0
	*lp = testLocks
	mlockTestThread.Context.SetGRegister(g0) // g0 — the leak shape
	mlockCheckpointSave(&mlockTestThread)
	foreignSaw = *lp // RED without the fix: == 3; GREEN: == 0
	stashCount = savedG0MLocks
	stashPtrOK = savedG0MLocksPtr == wantPtr

	// RE-ARM data contract: writing the stash back restores the EXACT count and
	// clears the slot — mirroring the asm at load_context_and_iretq (the asm
	// instructions themselves are objdump/batch-validated, see the doc comment).
	if savedG0MLocks != 0 && savedG0MLocksPtr != 0 {
		*(*int32)(unsafe.Pointer(savedG0MLocksPtr)) = savedG0MLocks
		savedG0MLocks = 0
	}
	rearmed = *lp
	slotCleared = savedG0MLocks == 0

	// Leave no trace: restore the real field and the checkpoint globals.
	*lp = orig
	savedG0MLocks = saveSlot
	savedG0MLocksPtr = savePtr
	RestoreIRQs(daif)

	if !gateHeld {
		klog.Errf("[mlock-selftest] FAIL: a non-g0 outgoing context was checkpointed (gate broken — would zero a foreign m's locks)\n")
		panic("runMLockCheckpointSelfTest: gate — only g0 may be checkpointed")
	}
	if foreignSaw != 0 {
		klog.Errf("[mlock-selftest] FAIL (RED): foreign reader saw g0.m.locks=%d, want 0 — the save did not hide the leaked count\n", foreignSaw)
		panic("runMLockCheckpointSelfTest: foreign window not zeroed")
	}
	if stashCount != testLocks || !stashPtrOK {
		klog.Errf("[mlock-selftest] FAIL: checkpoint stash count=%d (want %d), ptrOK=%v — asm restore would re-arm wrong\n", stashCount, testLocks, stashPtrOK)
		panic("runMLockCheckpointSelfTest: checkpoint stash wrong")
	}
	if rearmed != testLocks || !slotCleared {
		klog.Errf("[mlock-selftest] FAIL: re-arm restored g0.m.locks=%d (want %d), slotCleared=%v\n", rearmed, testLocks, slotCleared)
		panic("runMLockCheckpointSelfTest: re-arm contract wrong")
	}
	klog.Criticalf("[MLOCK]", "[mlock-selftest] OK — g0 switch-out hides m.locks (foreign sees 0), stashes count+&slot, re-arm restores exactly; non-g0 gate holds. (asm restore at load_context_and_iretq: objdump + TCG batch)\n")
}
