//go:build arm64

package main

// [MAZ-179 probe — NOT FOR MERGE, tier 17] Mislabeled-continuation witness.
//
// Tier-16's enriched E:00 dump proved (4/4 fatal boots, identical fields)
// that a ThreadContext pairing a kernel-thread-style EL1t SPSR
// (0x80000004, IRQs unmasked) with a PC inside the exception-handler blob
// was resumed via the ctx→frame→ERET path: SPSR=0x80000004, SVD=0,
// SP_EL0==SP_EL1==frame base, CurrentThread varying per boot. Handler code
// only ever executes at EL1h, so ELR-in-handler-blob + M[3:0]!=EL1h is a
// poisoned pair by definition.
//
// This witness fires at both ends:
//   save side  — SaveContextFromFrame / SaveCurrentThreadContext capture
//                such a pair into a ThreadContext (names the save site
//                while the writer's frame is live);
//   resume side — badResumeRIP (all four resume-guard sites: yield,
//                pickup, preempt, ctxswitch) detects the pair on the
//                context about to be loaded, and the existing
//                badResumeHalt halts BEFORE the poisoned ERET. If tier-17
//                boots halt here and the shepherd heap corruption
//                disappears, the mislabeled resume IS the writer.

import (
	"sync/atomic"

	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// maz179VTBounds returns [lo, hi) of the exception-vector TEXT blob.
// Implemented in maz179_ctx_asm_arm64.s.
func maz179VTBounds() (lo, hi uintptr)

// maz179YieldProbe is the tier-21 handler-context yield witness, implemented
// in exceptions_arm64.s and called ONLY from YieldToReadyThread's assembly
// (it takes the continuation ELR in R0, outside the Go ABI). Declared here so
// the asm TEXT has a matching Go declaration.
func maz179YieldProbe()

// maz179YieldResumeCheck verifies that a handler-context yield resumed with
// SP_EL1 exactly where it was suspended, and retires the chain-depth count.
// Implemented in exceptions_arm64.s; call immediately after every
// YieldToReadyThread() return. No-op for ordinary EL1t yields.
func maz179YieldResumeCheck()

// Published by initResumeGuardBounds at InitThreads time; zero until then
// (checks are skipped while zero — nothing schedules before InitThreads).
var maz179VTLo, maz179VTHi uint64

//go:nosplit
func maz179CtxInVT(pc uint64) bool {
	return maz179VTLo != 0 && pc >= maz179VTLo && pc < maz179VTHi
}

// maz179CtxCheck records a poisoned (ELR, SPSR) pair. site: 1=SaveContext-
// FromFrame 2=SaveCurrentThreadContext 3=badResumeRIP. Serial marker
// !CTX<site> is capped at 8 emissions (PollWrite busy-spins; a flood would
// stall IRQ-off contexts).
//
//go:nosplit
func maz179CtxCheck(elr, spsr uint64, site uint32, tid int64) {
	if !maz179CtxInVT(elr) || spsr&0xF == 5 {
		return
	}
	if atomic.CompareAndSwapUint32(&proc.CtxBadFirstSite, 0, site) {
		atomic.StoreUint64(&proc.CtxBadFirstELR, elr)
		atomic.StoreUint64(&proc.CtxBadFirstSPSR, spsr)
		atomic.StoreUint64(&proc.CtxBadFirstTID, uint64(tid))
	}
	var n uint64
	if site == 3 {
		n = atomic.AddUint64(&proc.CtxBadResumes, 1)
	} else {
		n = atomic.AddUint64(&proc.CtxBadSaves, 1)
	}
	if n <= 8 {
		serial.PollWrite('!')
		serial.PollWrite('C')
		serial.PollWrite('T')
		serial.PollWrite('X')
		serial.PollWrite(byte('0' + site))
	}
}
