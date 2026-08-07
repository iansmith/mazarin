//go:build arm64

package main

// badResumeRIP preserves the prior RIP==0 guard on ARM64, plus the
// [MAZ-179 tier-17] mislabeled-continuation check.
//
// (The old comment here claimed ARM64 "has never exhibited the MAZ-136
// kernel-mode resume corruption" — refuted 2026-08-07: tier-16 E:00 dumps
// proved a resumed context pairing an EL1t SPSR with a PC inside the
// exception-handler blob. Handler code only executes at EL1h, so that pair
// is poisoned by definition; halting here stops the ERET before it fires.)
//
//go:nosplit
func badResumeRIP(next *Thread) bool {
	if next.Context.GetPC() == 0 {
		return true
	}
	if maz179CtxInVT(next.Context.ELR) && next.Context.SPSR&0xF != 5 {
		maz179CtxCheck(next.Context.ELR, next.Context.SPSR, 3, int64(next.TID))
		return true
	}
	return false
}

// initResumeGuardBounds publishes the exception-vector blob bounds for the
// tier-17 witness (the asm-level IRETQ guards remain amd64-only).
//
//go:nosplit
func initResumeGuardBounds() {
	lo, hi := maz179VTBounds()
	maz179VTLo, maz179VTHi = uint64(lo), uint64(hi)
}
