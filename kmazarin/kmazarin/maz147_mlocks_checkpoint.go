package main

import (
	"unsafe"

	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
)

// MAZ-147 — checkpoint runtime `m.locks` across the kernel's raw context switch so
// a leaked count on the borrowed g0/m0 is never observed by foreign code that
// reuses or borrows m0. ARCH-NEUTRAL half (save + globals + selftest); the per-arch
// asm RESTORE and the per-arch preempt SKIP-guard (`g0PreemptHoldsMLocks`) live in
// maz147_mlocks_checkpoint_{amd64,arm64}.go.
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
// Fix: when g0 is switched OUT carrying `m.locks > 0`, stash the count and zero the
// live `m.locks` (m0 then presents 0 to any foreign reuse); when g0 is switched
// back IN, restore it. Zeroing is safe because the *actual* runtime lock g0 holds
// stays locked in memory (contenders still block correctly) — only the
// non-preemption *hint* is hidden for the foreign window, which foreign code does
// not need.
//
// SAVE/RESTORE SPLIT (design doc §8): kmazarin has TWO independent switch funnels —
// `doContextSwitchImpl` (directed/syscall switches) and `checkThreadPreemptionImpl`
// (timer preemption / priority-wake / idle pickup) — that converge only at the
// per-arch assembly resume chokepoint. g0's usleep→nanosleep is SAVED here (called
// from `doContextSwitchImpl`), but g0's thread is marked sleeping, woken by the
// timer, and RESUMED by the preemption funnel — which never returns through
// `doContextSwitchImpl`. So the RESTORE must live at the asm chokepoint:
//   - amd64: `exceptions_amd64.s:load_context_and_iretq` (single funnel chokepoint),
//     keyed on `ctx.R14 == kmazarinG0Addr`.
//   - arm64: `mlockRearmFromFrame` in `exceptions_arm64.s`, BL'd from each
//     `CTX_RESTORE_TO_FRAME` site, keyed on the restored `frame[X28] == kmazarinG0Addr`.
// Restoring at only `doContextSwitchImpl` (the first cut) missed the dominant resume
// path and lost the count.
//
// SCOPE: keyed on the outgoing context's `g == kmazarinG0Addr` (the always-mapped
// kernel g0), so we only ever dereference g0 — never an arbitrary/sentinel g (the
// GSPMM #PF lesson). The evidence shows only g0 leaks (the crash signature is always
// g0/m0); regular goroutines don't, so they need no checkpoint.
//
// SINGLE GLOBAL SLOT: one g0 ⇒ one checkpoint. g0 cannot be switched out twice while
// already out (between save and resume g0 is not running), so the stash is always
// consumed by the very next g0 load before a new save can occur. A borrowed-m0
// syscall ENTRY is not an asm-chokepoint resume of g0, so the borrow correctly
// observes the zeroed count without touching the slot.
//
// SMP: single global slot, CPU-0/m0-scoped (matches the MAZ-143 amd64 CPU-0 scope →
// MAZ-142 for full SMP). Save runs under schedulerLock+IRQs-off; the asm restore
// runs IRQ-off before the return — no concurrent access on a single CPU.

var (
	// savedG0MLocks is g0's m.locks while it is switched out (0 = nothing
	// checkpointed). The per-arch asm restore consumes it (re-arms g0.m.locks and
	// clears this back to 0).
	savedG0MLocks int32
	// savedG0MLocksPtr is &g0.m.locks, precomputed in Go at save time so the
	// nosplit/no-frame asm restore can re-arm the count WITHOUT recomputing the
	// cross-package kirq g→m / m→locks offsets in assembly. g0.m (== m0) is stable,
	// so the pointer stays valid between the save and the matching resume.
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

// mlockCheckpointSave is called in doContextSwitchImpl when g0 is being switched
// OUT to a real next thread (past the newThread==nil guard). If the outgoing
// context is g0 carrying m.locks>0, stash the count + its address and present 0 on
// m0 for the foreign window. The matching re-arm happens at the per-arch asm resume
// chokepoint (see file header). Arch-neutral: GetGRegister() returns the g-register
// (amd64 R14 / arm64 X28) and g0MLocksPtr uses the shared kirq offsets.
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

// runMLockCheckpointSelfTest is the deterministic RED→GREEN for MAZ-147. It proves
// the SAVE-side invariant — the half that is RED without the fix and GREEN with it:
// when g0 is switched out holding m.locks, a FOREIGN reader of g0.m.locks sees 0 (so
// a borrowed/reused m0 never trips schedule:holding-locks), while the count AND its
// address are stashed for the resume-time re-arm. It also proves the GATE (a non-g0
// outgoing context is never checkpointed) and the re-arm DATA CONTRACT (writing
// savedG0MLocks through savedG0MLocksPtr restores the exact count to g0.m.locks).
//
// What this does NOT cover: the literal per-arch asm restore instructions — a
// context load can't be unit-invoked (it returns/erets away). Those are covered by
// the objdump verification + the TCG batch (THE gate), where real g0 nanosleep→resume
// cycles exercise the asm and assert no schedule:holding-locks. This test pins the Go
// contract the asm consumes (correct pointer, correct value, correct gate).
//
// Arch-neutral: no frame reads (those are in the per-arch g0PreemptHoldsMLocks). It
// borrows the real g0.m.locks for a tiny IRQ-masked window with NO allocation/klog
// inside, and fully restores it (along with the checkpoint globals) before the
// assertions, which klog/panic (and allocate).
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
	orig := *lp                 // real g0.m.locks (0 at this quiescent boot point)
	saveSlot := savedG0MLocks   // preserve any live checkpoint (there should be none)
	savePtr := savedG0MLocksPtr // ...and its pointer
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
	// clears the slot — mirroring the per-arch asm restore (the asm instructions
	// themselves are objdump/batch-validated, see the doc comment).
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
	klog.Criticalf("[MLOCK]", "[mlock-selftest] OK — g0 switch-out hides m.locks (foreign sees 0), stashes count+&slot, re-arm restores exactly; non-g0 gate holds. (asm restore: objdump + TCG batch)\n")
}
