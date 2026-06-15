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
	gspBytes([]byte("\nGSPMM g="))
	gspHex(g)
	gspBytes([]byte(" sp="))
	gspHex(sp)
	gspBytes([]byte(" rip="))
	gspHex(rip)
	gspBytes([]byte(" stk=["))
	gspHex(lo)
	gspBytes([]byte(","))
	gspHex(hi)
	gspBytes([]byte(")\n"))
}

//go:nosplit
func gspBytes(b []byte) {
	for i := 0; i < len(b); i++ {
		serial.PollWrite(b[i])
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

	// Case 1 (the bug): SP just below g0.stack.lo ⇒ outside ⇒ detector MUST fire.
	before := atomic.LoadUint64(&dbgGSPMismatch)
	gspRunSyntheticKernelSave(g0, lo-0x1000)
	caught := atomic.LoadUint64(&dbgGSPMismatch) - before

	// Case 2 (control): SP inside g0's stack ⇒ detector MUST stay silent.
	before2 := atomic.LoadUint64(&dbgGSPMismatch)
	gspRunSyntheticKernelSave(g0, lo+0x80)
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
	badSkip := gspUnsafeKernelResume(gspBuildSyntheticKernelFrame(g0, lo-0x1000))
	goodSkip := gspUnsafeKernelResume(gspBuildSyntheticKernelFrame(g0, lo+0x80))
	if !badSkip || goodSkip {
		klog.Errf("[gsp-selftest] FAIL: guard badSkip=%v (want true), goodSkip=%v (want false)\n", badSkip, goodSkip)
		panic("runGSPMismatchSelfTest: fix-C (g,SP) guard predicate wrong")
	}
	klog.Criticalf("[GSP]", "[gsp-selftest] OK — (g,SP) detector + fix-C guard flag g0 out-of-stack SP, ignore in-stack SP\n")
}

// gspUnsafeKernelResume is FIX C — the (g,SP)-consistency guard on the raw kernel
// context switch. It reports whether the exception frame at framePtr captured
// thread 0 inside runtime.morestack's g→g0 / SP-switch transient (asm_amd64.s:678):
// a KERNEL-mode interrupt (CS==kernelCS) whose per-exception-frame TLS-g is the
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
	tlsg := *(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameTLSGOff))
	if tlsg != g0 {
		return false // not the g==g0 transient
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
// KERNEL exception frame whose per-exception-frame TLS-g slot is g0 and whose saved
// RSP (frame[19]) is sp, and returns its framePtr. Mirrors saveFromSyntheticKernelFrame's
// layout. Used by both the detector check (via SaveContextFromFrame) and the fix-C
// guard check (via gspUnsafeKernelResume).
func gspBuildSyntheticKernelFrame(g0, sp uint64) uintptr {
	const extWords = excFrameExtSize / 8
	backing := ctxTestBacking[:extWords+21] // stable global buffer — see ctxTestBacking
	frame := backing[extWords:]
	for i := range frame {
		frame[i] = ctxFrameSentinel + uint64(i)
	}
	frame[13] = g0       // R14 fallback (unused on the valid-TLSG path)
	frame[17] = kernelCS // kernel branch
	frame[19] = sp       // RSP — the (g,SP) the detector/guard checks against g0.stack

	framePtr := uintptr(unsafe.Pointer(&frame[0]))
	*(*uint64)(unsafe.Pointer(framePtr - excFrameExtSize + excFrameTLSGOff)) = g0 // per-frame TLS-g = g0
	fillXMMSlotSentinel(framePtr)
	return framePtr
}

// gspRunSyntheticKernelSave runs the REAL SaveContextFromFrame against the synthetic
// kernel frame (detector path). Reuses ctxTestThread; caller holds gspSelftestActive.
func gspRunSyntheticKernelSave(g0, sp uint64) {
	framePtr := gspBuildSyntheticKernelFrame(g0, sp)
	daif := SaveAndDisableIRQs()
	oldThread := GetCurrentThread()
	ctxTestThread.Context = ThreadContext{}
	SetCurrentThreadGlobal(&ctxTestThread)
	SaveContextFromFrame(framePtr)
	SetCurrentThreadGlobal(oldThread)
	RestoreIRQs(daif)
}
