//go:build amd64

package main

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/serial"
)

// MAZ-143 RED detector — (g,SP)-consistency check on the raw kernel context save.
//
// The kernel-worker `morestack on g0` is the raw kmazarin context switch
// (device-IRQ priority-wake / timer CheckThreadPreemption) checkpointing thread
// 0's worker goroutine at an unsafe transient: inside `runtime.morestack`'s own
// g→g0 / SP-switch window, `g` (and the captured kernel TLS-g) is already m0.g0
// but the interrupted `SP` is still on the goroutine HEAP stack. SaveContextFromFrame
// faithfully captures `(g=g0, SP=heap)`; load_context_and_iretq faithfully restores
// it; the next prologue sees `SP < g0.stackguard0` → `morestack on g0`. MAZ-139 made
// the save FAITHFUL, but faithful ≠ safe for this transient.
//
// This detector flags the unsafe save the instant it happens (a fatal
// `morestack on g0` throw will not reach OnShepherdExit, so we emit on the spot
// via the nosplit raw UART). The same predicate — SP outside the captured g's
// [stack.lo, stack.hi) — becomes the fix-C skip-guard once the mechanism is
// confirmed. Detection only here; no behavioural change.
//
// amd64-only: ARM64 has a single g home (X28) and a per-thread kernel stack
// (SP_EL0), so it cannot manufacture this (g,SP) split — documented divergence,
// see feedback_amd64_arch_divergence.md.

var (
	dbgGSPMismatch uint64 // total (g,SP)-inconsistent kernel saves observed
	dbgGSPLastG    uint64 // most recent captured g (== g0 in the bug)
	dbgGSPLastSP   uint64 // most recent interrupted SP (heap, in the bug)
	dbgGSPLastRIP  uint64 // most recent interrupted RIP (inside runtime.morestack)
	dbgGSPLastLo   uint64 // g.stack.lo at the time
	dbgGSPLastHi   uint64 // g.stack.hi at the time
	dbgGSPSkipped  uint64 // fix C: raw context switches deferred by gspUnsafeKernelResume
	gspEmitCount   uint32 // rate-limit on the raw-serial emit
	// gspSelftestActive suppresses the raw-serial GSPMM emit while the synthetic
	// RED selftest drives recordGSPMismatchKernel deliberately — otherwise the
	// selftest's expected fire would print a GSPMM line that the RED harness
	// (which greps serial for GSPMM) would misread as a NATURAL hit. The counter
	// still increments, which is what the selftest asserts on.
	gspSelftestActive uint32
)

const gspEmitMax = 48 // emit at most this many GSPMM lines, then count-only

// MAZ-143 EGTLS tripwire — the SILENT `morestack on g0` (design §12), now FIXED + guarded.
// Distinct from the dbgGSP* family above (which catches the PREEMPTING SaveContextFromFrame
// capture): this is the NO-PREEMPT kernel `exception_return` path (eret_kernel_tls,
// exceptions_amd64.s). It used to restore kernel TLS-g from the STALE frame R14 (g0) in the
// ~1-instruction window after a g-clobbering call (before the compiler's `MOVQ FS:-8,R14`
// reload), manufacturing (TLS-g=g0) on a live heap-stack goroutine → the next growth prologue
// threw. §12.4 FIX: eret_kernel_tls now restores TLS-g from the faithful per-frame captured
// slot instead. These counters are the PERMANENT tripwire (like MAZ-139's D2 canary): the asm
// increments egtlsCorruptCount each time it CORRECTS a window (frame R14==g0 AND slot==curg)
// and one-shot-marks the first to COM1. egtlsCorruptCount>0 with NO `morestack on g0` proves
// the fix is live and protecting. asm-mutated with LOCK / plain stores; read with atomics.
var (
	egtlsCorruptCount uint64 // no-preempt eret windows the §12.4 fix corrected (restored curg, not g0)
	egtlsLastSlot     uint64 // last corrected window: the curg we faithfully restored (was → g0)
	egtlsLastRIP      uint64 // last corrected window: interrupted RIP (the `MOVQ FS:-8,R14` reload site)
	egtlsLastRSP      uint64 // last corrected window: interrupted RSP (a heap goroutine stack)
	egtlsMarked       uint64 // one-shot guard for the first-hit serial "EGTLS" marker
)

// dumpEGTLSStats surfaces the MAZ-143 silent-morestack tripwire counters. Called from the
// shepherd-exit dump (dumpPwakeRing) alongside dumpNestStats. The durable live signal is the
// one-shot "EGTLS" serial marker emitted by eret_kernel_tls the first time the fix corrects a
// window — a normal boot need not reach a shepherd-exit dump, and a real crash halts before it.
func dumpEGTLSStats() {
	klog.Criticalf("egtls", "[egtls] silent-morestack detector: corrupt=%d lastSlot=%#x lastRIP=%#x lastRSP=%#x\n",
		atomic.LoadUint64(&egtlsCorruptCount), atomic.LoadUint64(&egtlsLastSlot),
		atomic.LoadUint64(&egtlsLastRIP), atomic.LoadUint64(&egtlsLastRSP))
}

// recordGSPMismatchKernel checks whether the interrupted SP lies within the
// kernel g0's stack bounds, but ONLY when the captured g IS g0 — the exact
// `morestack on g0` precondition (runtime.morestack sets TLS-g = m.g0 before
// switching SP). g.stack is the first field of runtime.g (stack.lo @ +0,
// stack.hi @ +8). A consistent save returns immediately; an inconsistent one is
// recorded and (rate-limited) emitted to COM1.
//
// SAFETY: we dereference ONLY kmazarinG0Addr (the always-mapped kernel g0),
// never the arbitrary captured g. In early boot / clone-child bringup the
// per-frame TLS-g slot can hold a canonical-low but UNMAPPED value (e.g.
// 0x0000555555555000) that passes gLooksValid yet #PFs on deref. Gating on
// g==g0 sidesteps that and targets only the documented bug (goroutine 1 / m0).
//
//go:nosplit
func recordGSPMismatchKernel(g, sp, rip uint64) {
	g0 := kmazarinG0Addr
	if g0 == 0 || g != g0 {
		return // not the g==g0 transient — nothing to check, nothing unsafe to deref
	}
	lo := *(*uint64)(unsafe.Pointer(uintptr(g0)))     // g0.stack.lo
	hi := *(*uint64)(unsafe.Pointer(uintptr(g0) + 8)) // g0.stack.hi
	if sp >= lo && sp < hi {
		return // consistent — running legitimately on the g0 stack
	}
	atomic.AddUint64(&dbgGSPMismatch, 1)
	atomic.StoreUint64(&dbgGSPLastG, g)
	atomic.StoreUint64(&dbgGSPLastSP, sp)
	atomic.StoreUint64(&dbgGSPLastRIP, rip)
	atomic.StoreUint64(&dbgGSPLastLo, lo)
	atomic.StoreUint64(&dbgGSPLastHi, hi)
	if atomic.LoadUint32(&gspSelftestActive) != 0 {
		return // synthetic selftest drove this fire — count it, but don't emit
	}
	if atomic.AddUint32(&gspEmitCount, 1) <= gspEmitMax {
		gspEmit(g, sp, rip, lo, hi)
	}
}

// gspEmit writes one "GSPMM" marker line to COM1 via the nosplit raw UART.
// Flat (no nested string helpers) to keep the nosplit call chain shallow.
//
//go:nosplit
func gspEmit(g, sp, rip, lo, hi uint64) {
	gspStr("\nGSPMM g=")
	gspHex(g)
	gspStr(" sp=")
	gspHex(sp)
	gspStr(" rip=")
	gspHex(rip)
	gspStr(" stk=[")
	gspHex(lo)
	gspStr(",")
	gspHex(hi)
	gspStr(")\n")
}

// gspStr writes a string's bytes to COM1 via the nosplit raw UART. Indexing a
// string (s[i]) is allocation-free — unlike []byte(s), which can compile to a
// non-nosplit stringtoslicebyte call if escape analysis ever decides the slice
// escapes (a latent nosplit-stack-overflow at link time). Matches the
// single-PollWrite pattern of kmazarin's other nosplit serial emitters.
//
//go:nosplit
func gspStr(s string) {
	for i := 0; i < len(s); i++ {
		serial.PollWrite(s[i])
	}
}

//go:nosplit
func gspHex(v uint64) {
	serial.PollWrite('0')
	serial.PollWrite('x')
	for shift := 60; shift >= 0; shift -= 4 {
		nib := byte((v >> uint(shift)) & 0xF)
		if nib < 10 {
			serial.PollWrite('0' + nib)
		} else {
			serial.PollWrite('a' + nib - 10)
		}
	}
}

// runGSPMismatchSelfTest is a DETERMINISTIC boot RED/GREEN for the MAZ-143
// (g,SP)-mismatch detector. It feeds the bug's exact, independently-established
// signature — kernel save with TLS-g == g0 and the saved RSP OUTSIDE g0's stack
// (the runtime.morestack g→g0/SP-switch window, asm_amd64.s:678) — through the
// REAL SaveContextFromFrame, non-invasively (same pattern + shared scratch as
// runContextMarshalSelfTest), and asserts the detector fires; a control with RSP
// INSIDE g0's stack asserts it stays silent. The serial emit is suppressed while
// driving (gspSelftestActive) so the live RED harness doesn't misread the
// selftest's expected fire as a natural hit.
//
//   - RED  (detector absent / predicate wrong): the (g0, out-of-stack SP) save is
//     NOT flagged → fails here, and in the wild it is the `morestack on g0` crash.
//   - GREEN (detector correct): bad save flagged exactly once; good save ignored.
//
// When fix C lands (skip the switch on this predicate), this test is extended to
// assert the unsafe switch is SKIPPED. Gated by the same kernel.toml
// ctx_marshal_test toggle (called from main alongside runContextMarshalSelfTest).
func runGSPMismatchSelfTest() {
	// kmazarinG0Addr is established in InitKernel (threads.go), long before this
	// gated boot point, and a valid kernel g0 always has valid stack bounds — so
	// these are should-never-happen conditions. FAIL loud (matching every other
	// path in this harness) rather than silently skipping, which a reader could
	// mistake for a pass.
	g0 := kmazarinG0Addr
	if g0 == 0 {
		klog.Errf("[gsp-selftest] FAIL: kmazarinG0Addr not set at selftest time\n")
		panic("runGSPMismatchSelfTest: kmazarinG0Addr not set")
	}
	lo := *(*uint64)(unsafe.Pointer(uintptr(g0)))
	hi := *(*uint64)(unsafe.Pointer(uintptr(g0) + 8))
	if lo == 0 || hi <= lo {
		klog.Errf("[gsp-selftest] FAIL: implausible g0 stack [%#x,%#x)\n", lo, hi)
		panic("runGSPMismatchSelfTest: implausible g0 stack bounds")
	}

	atomic.StoreUint32(&gspSelftestActive, 1)
	defer atomic.StoreUint32(&gspSelftestActive, 0)

	// Case 1 (the bug): TLS-slot=g0, SP just below g0.stack.lo ⇒ outside ⇒ detector MUST fire.
	before := atomic.LoadUint64(&dbgGSPMismatch)
	gspRunSyntheticKernelSave(g0, lo-0x1000, g0)
	caught := atomic.LoadUint64(&dbgGSPMismatch) - before

	// Case 2 (control): TLS-slot=g0, SP inside g0's stack ⇒ detector MUST stay silent.
	before2 := atomic.LoadUint64(&dbgGSPMismatch)
	gspRunSyntheticKernelSave(g0, lo+0x80, g0)
	falseFire := atomic.LoadUint64(&dbgGSPMismatch) - before2

	if caught != 1 || falseFire != 0 {
		klog.Errf("[gsp-selftest] FAIL: bad-SP caught=%d (want 1), good-SP fired=%d (want 0)\n", caught, falseFire)
		panic("runGSPMismatchSelfTest: (g,SP) detector predicate wrong")
	}

	// Fix C (the guard): the SAME predicate that the raw context-switch path
	// (checkThreadPreemptionImpl) consults must SKIP the bad (g0, out-of-stack)
	// frame and PASS the good (g0, in-stack) frame. This is the GREEN: a thread in
	// the morestack transient is never checkpointed → never resumes into
	// `morestack on g0`. (gspSelftestActive suppresses the live dbgGSPSkipped bump.)
	badSkip := gspUnsafeKernelResume(gspBuildSyntheticKernelFrame(g0, lo-0x1000, g0))
	goodSkip := gspUnsafeKernelResume(gspBuildSyntheticKernelFrame(g0, lo+0x80, g0))
	if !badSkip || goodSkip {
		klog.Errf("[gsp-selftest] FAIL: guard badSkip=%v (want true), goodSkip=%v (want false)\n", badSkip, goodSkip)
		panic("runGSPMismatchSelfTest: fix-C (g,SP) guard predicate wrong")
	}

	// Case B — the surviving NATURAL morestack-on-g0 (MAZ-143 reopened; first live
	// repro 2026-06-16, 1/12 TCG on the merged fix-C build, with NO GSPMM). A KERNEL
	// goroutine's g is a HIGH kernel VA (0xffff8001…) that gLooksValid REJECTS, so
	// SaveContextFromFrame's kernel branch ALWAYS falls back to Context.TLSG = R14 and
	// never runs the detector. In runtime.morestack R14 becomes g0 a few instructions
	// before TLS-g does; an IRQ captured there has R14=g0 but the per-frame TLS slot
	// still = the goroutine g (high, !gLooksValid). The save reconstructs
	// Context.TLSG = R14 = g0 → load_context_and_iretq writes TLS-g = g0 → morestack
	// reads TLS-g == m.g0 → throw. The original fix-C guard checked the RAW slot
	// (high VA ≠ g0) and so NEVER skipped this window — the gap this fix closes.
	const caseBHighVAg = 0xffff800148008000 // mimics a real kernel goroutine g (high VA, !gLooksValid, ≠ g0)
	beforeB := atomic.LoadUint64(&dbgGSPMismatch)
	gspRunSyntheticKernelSave(g0, lo-0x1000, caseBHighVAg)
	caseBDetector := atomic.LoadUint64(&dbgGSPMismatch) - beforeB
	caseBTLSG := ctxTestThread.Context.TLSG
	// Invariants of the EXISTING save (true before AND after the fix): the R14 fallback
	// reconstructs the crash precondition (Context.TLSG = g0), and the detector stays
	// silent (gLooksValid(slot) is false) — exactly why the live crash showed no GSPMM.
	if caseBTLSG != g0 || caseBDetector != 0 {
		klog.Errf("[gsp-selftest] FAIL: Case-B save TLSG=%#x (want g0=%#x), detector fired=%d (want 0) — mechanism assumption broken\n", caseBTLSG, g0, caseBDetector)
		panic("runGSPMismatchSelfTest: Case-B save/detector assumption wrong")
	}
	// RED→GREEN: the guard MUST skip this window. Unfixed (raw-slot check) → false (RED);
	// fixed (effective g = gLooksValid(slot)?slot:R14) → true (GREEN).
	caseBSkip := gspUnsafeKernelResume(gspBuildSyntheticKernelFrame(g0, lo-0x1000, caseBHighVAg))
	if !caseBSkip {
		klog.Errf("[gsp-selftest] FAIL (RED): Case-B (R14=g0, !gLooksValid TLS slot, SP off-g0) NOT skipped — guard must use the effective g (gLooksValid(slot)?slot:R14) the save restores, not the raw slot (MAZ-143 fix-C gap)\n")
		panic("runGSPMismatchSelfTest: Case-B guard gap — guard must mirror the save's effective g")
	}

	klog.Criticalf("[GSP]", "[gsp-selftest] OK — (g,SP) detector + fix-C guard flag g0 out-of-stack SP (valid-slot AND R14-fallback/Case-B windows), ignore in-stack SP\n")
}

// runCloneTLSGSelfTest PROVES MAZ-143 confirmation (a): a clone child's Context.TLSG
// carries the PARENT's g — a HIGH-canonical value — NOT the child's. The clone path
// (doContextSwitchImpl:4210 `child.Context = parent.Context`, then SetGRegister(childG)
// which sets R14 ONLY at :4229) never overrides ctx.TLSG, and SetCloneTLS sets FSBase
// only. Consequences this test pins down deterministically (no perturbation; uses the
// REAL SetupForCloneChild + SetGRegister + struct-copy):
//   - A naive "accept high-canonical" gLooksValid WIDENING would make SaveContextFromFrame
//     trust ctx.TLSG = parentG → the child resumes with the PARENT's g → GC/#GP corruption
//     (the failure save_context_amd64.go:4233-4237 warns about). So the leading fix as
//     first stated is UNSAFE.
//   - The refined rule "trust the TLS slot only when R14 == g0 (the systemstack-exit
//     signature)" trusts R14 = childG here (a clone child's R14 != g0) → SAFE.
//
// This is the linchpin for the dual-home fix design (design/maz143-dual-home-g-raw-context-save.md).
func runCloneTLSGSelfTest() {
	g0 := kmazarinG0Addr
	if g0 == 0 {
		klog.Errf("[clone-tlsg-selftest] FAIL: kmazarinG0Addr not set\n")
		panic("runCloneTLSGSelfTest: kmazarinG0Addr not set")
	}
	// Two distinct, plausible HIGH-canonical kernel g pointers (≠ g0, ≠ each other),
	// mimicking real kernel goroutine g's at 0xffff8001…
	const parentG uint64 = 0xffff800148008000
	const childG uint64 = 0xffff800148009000

	// Parent: a consistent running goroutine — both homes = parentG.
	var parent ThreadContext
	parent.R14 = parentG
	parent.TLSG = parentG

	// Build the clone child exactly as doContextSwitchImpl does for CloneNeedsParentRegs:
	var child ThreadContext
	child.SetupForCloneChild(0x7000, 0x45800000, childG, 0x202) // sets R14=childG, NOT TLSG (:235)
	savedChildG := child.GetGRegister()                         // childG (:4207)
	child = parent                                              // :4210 full copy ⇒ TLSG := parentG
	child.SetGRegister(savedChildG)                             // :4229 R14 := childG (TLSG untouched)

	// (a) The clone child's TLSG is the PARENT's g; R14 is the child's.
	if child.TLSG != parentG || child.GetGRegister() != childG {
		klog.Errf("[clone-tlsg-selftest] FAIL: child TLSG=%#x (want parentG=%#x), R14=%#x (want childG=%#x)\n",
			child.TLSG, parentG, child.GetGRegister(), childG)
		panic("runCloneTLSGSelfTest: clone g-home assumption broken")
	}

	// canonical = the NAIVE widened gLooksValid (accepts low OR high canonical).
	canon := func(v uint64) bool { return v != 0 && (v < 0x0000800000000000 || v >= 0xFFFF800000000000) }
	// Naive widened rule: trust TLS whenever canonical → picks parentG (WRONG, regresses clone).
	naiveEffG := child.R14
	if canon(child.TLSG) {
		naiveEffG = child.TLSG
	}
	// Refined rule: trust TLS only when R14 is the stale g0 (systemstack-exit) → picks childG (SAFE).
	refinedEffG := child.R14
	if child.R14 == g0 && canon(child.TLSG) && child.TLSG != g0 {
		refinedEffG = child.TLSG
	}
	if naiveEffG != parentG {
		klog.Errf("[clone-tlsg-selftest] FAIL: naive widened rule effG=%#x, expected to (wrongly) pick parentG=%#x\n", naiveEffG, parentG)
		panic("runCloneTLSGSelfTest: naive-rule demonstration wrong")
	}
	if refinedEffG != childG {
		klog.Errf("[clone-tlsg-selftest] FAIL: refined (R14==g0) rule effG=%#x, want childG=%#x — would regress clone\n", refinedEffG, childG)
		panic("runCloneTLSGSelfTest: refined rule does not protect clone bringup")
	}
	klog.Criticalf("[GSP]", "[clone-tlsg-selftest] OK — clone child TLSG=parentG (high-canonical); naive widening picks parentG (REGRESSES clone), R14==g0 rule picks childG (SAFE)\n")
}

// gspUnsafeKernelResume is FIX C — the (g,SP)-consistency guard on the raw kernel
// context switch. It reports whether the exception frame at framePtr captured
// thread 0 inside runtime.morestack's g→g0 / SP-switch transient (asm_amd64.s:678):
// a KERNEL-mode interrupt (CS==kernelCS) whose EFFECTIVE restored g — the same
// gLooksValid(slot)?slot:R14 SaveContextFromFrame will write into Context.TLSG — is the
// kernel g0 (kmazarinG0Addr) but whose saved RSP (frame[19]) is OUTSIDE g0's stack.
// Faithfully checkpointing and later restoring that (g,SP) resumes a prologue with
// SP < g0.stackguard0 → `morestack on g0`. checkThreadPreemptionImpl calls this
// FIRST and skips the switch when it returns true, deferring preemption ~one tick;
// the thread clears the 1-instruction window on resume, so there is no livelock.
//
// Why this IS the morestack-on-g0 root, and why it is amd64-only: amd64 keeps g in
// TWO homes (R14 + TLS [FS_BASE-8]) that the kmazarin save/restore reconstructs
// independently, so checkpointing the morestack window manufactures a both-homes-g0
// / SP-on-goroutine-stack desync on resume → the next prologue enters morestack with
// g==m.g0. ARM64's single g-home (X28) makes the identical window benign (restored
// verbatim, resumes cleanly) — full cross-arch rationale in maz143_gspmm_arm64.go.
//
// SCOPE (SMP): checks the single global kmazarinG0Addr (CPU-0 / m0's g0), so a
// morestack window on a SECONDARY CPU's kernel goroutine (g0 ≠ kmazarinG0Addr) is NOT
// matched. Acceptable today — the kernel-worker loadSegment path is CPU-0 / thread-0
// bound and x86 SMP is not the live path — but a fully-SMP x86 kernel must extend this
// to every per-CPU g0. Tracked under MAZ-142 (x86 SMP per-CPU state), same bucket as
// the single-CPU-assuming pwake ring / counters.
// SAFETY: dereferences only kmazarinG0Addr (always mapped), never the captured g.
//
//go:nosplit
func gspUnsafeKernelResume(framePtr uintptr) bool {
	if framePtr == 0 {
		return false
	}
	frame := (*[21]uint64)(unsafe.Pointer(framePtr))
	if frame[17] != kernelCS {
		return false // user-mode resume — handled by the dual-home TLSG restore, not this
	}
	g0 := kmazarinG0Addr
	if g0 == 0 {
		return false // pre-init: no kernel g0 yet
	}
	// Predict the SAME effective g that SaveContextFromFrame will RESTORE, not the raw
	// slot. Its kernel branch sets Context.TLSG = gLooksValid(slot) ? slot : R14
	// (save_context_amd64.go), and load_context_and_iretq writes that into TLS-g, which
	// is the home runtime.morestack's badmorestackg0 reads. Kernel goroutine g's are HIGH
	// kernel VAs (0xffff8001…) that gLooksValid REJECTS, so for a real kernel goroutine the
	// save ALWAYS falls back to R14. Checking only the raw slot (the original fix C) misses
	// the morestack sub-window where R14 has already become g0 but the slot still holds the
	// (high-VA, gLooksValid-false) goroutine g — the surviving natural `morestack on g0`
	// (MAZ-143 reopened: first live repro 2026-06-16, 1/12 TCG on the merged build, no GSPMM
	// because the detector likewise lives only in the gLooksValid-true branch).
	slot := *(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameTLSGOff))
	effG := slot
	if !gLooksValid(slot) {
		effG = frame[13] // R14 — mirror SaveContextFromFrame's fallback
	}
	if effG != g0 {
		return false // the restored TLS-g will not be g0 — nothing unsafe to skip
	}
	lo := *(*uint64)(unsafe.Pointer(uintptr(g0)))     // g0.stack.lo
	hi := *(*uint64)(unsafe.Pointer(uintptr(g0) + 8)) // g0.stack.hi
	sp := frame[19]                                   // interrupted RSP
	if sp >= lo && sp < hi {
		return false // SP legitimately on the g0 stack — safe to checkpoint
	}
	if atomic.LoadUint32(&gspSelftestActive) == 0 {
		atomic.AddUint64(&dbgGSPSkipped, 1) // don't let the selftest pollute the live counter
	}
	return true
}

// gspBuildSyntheticKernelFrame populates the shared ctxTestBacking with a synthetic
// KERNEL exception frame whose per-exception-frame TLS-g slot is tlsSlot, whose R14
// (frame[13]) is g0 (the save's gLooksValid-false fallback home), and whose saved RSP
// (frame[19]) is sp; it returns the framePtr. Mirrors saveFromSyntheticKernelFrame's
// layout. Used by the detector check (via SaveContextFromFrame) and the fix-C guard
// check (via gspUnsafeKernelResume). Callers pass tlsSlot=g0 for the original
// valid-TLSG window, or a high-VA !gLooksValid value for the Case-B R14-fallback window.
func gspBuildSyntheticKernelFrame(g0, sp, tlsSlot uint64) uintptr {
	const extWords = excFrameExtSize / 8
	backing := ctxTestBacking[:extWords+21] // stable global buffer — see ctxTestBacking
	frame := backing[extWords:]
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[13] = g0       // R14 — the save's fallback home when the slot is !gLooksValid
	frame[17] = kernelCS // kernel branch
	frame[19] = sp       // RSP — the (g,SP) the detector/guard checks against g0.stack

	framePtr := uintptr(unsafe.Pointer(&frame[0]))
	*(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameTLSGOff)) = tlsSlot // per-frame TLS-g slot
	fillXMMSlotSentinel(framePtr)
	return framePtr
}

// gspRunSyntheticKernelSave runs the REAL SaveContextFromFrame against the synthetic
// kernel frame (detector path). Reuses ctxTestThread; caller holds gspSelftestActive.
func gspRunSyntheticKernelSave(g0, sp, tlsSlot uint64) {
	framePtr := gspBuildSyntheticKernelFrame(g0, sp, tlsSlot)
	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	ctxTestThread.Context = ThreadContext{}
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(framePtr)
	SetCurrentThreadGlobal(oldThread)
	RestoreIRQs(daif)
}
