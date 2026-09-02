//go:build arm64

package main

import "mazzy/kmazarin/proc"

// Resume guard (ARM64 side of MAZ-136's lineage; poisoned-pair check MAZ-196).
//
// ARM64 keeps g in a single home (X28), so it lacks amd64's dual-home save
// gap — but the claim that it "never exhibited kernel-mode resume corruption"
// was REFUTED 2026-08-07 (MAZ-179 tier-16): the MAZ-193 SPSR/SP hardcode
// saved poisoned continuations that ERET'd handler code at EL1t on an alias
// of the exception stack. The predicate (one definition, host-tested) lives
// in proc.BadResumeARM64: PC==0, plus the poisoned pair — a PC inside the
// exception-vector blob whose SPSR mode bits are not EL1h.
//
// Coverage, honestly (probe record, tiers 17/18): the MAZ-193 poison
// reached ERET via asm exception-return paths that never consult these
// Go-side resume sites — the four badResumeRIP call sites stayed silent
// while E:00 kept firing on the probe branch. With the producer fixed
// (MAZ-193), this guard is defence-in-depth for future producers routing
// through the scheduler; the asm consumption-waist (pre-ERET) guard is a
// separate ticket.

// vectorBlobLo/Hi bound the exception-vector handler blob. Zero until
// initResumeGuardBounds runs (the predicate skips the vector check then).
var (
	vectorBlobLo uint64
	vectorBlobHi uint64
)

// initResumeGuardBounds publishes the exception-vector blob bounds for the
// resume guard. Called from InitThreads, before IRQs are enabled. The end
// marker relies on linker symbol ordering, so sanity-check the span and
// fail open (bounds stay zero → guard degrades to PC==0) rather than halt
// healthy boots on a pathological layout.
//
//go:nosplit
func initResumeGuardBounds() {
	lo := uint64(GetExceptionVectorBase())
	hi := uint64(getExceptionVectorBlobEnd())
	// The blob is >2KB of vectors plus handler bodies, well under 1MB.
	if hi <= lo || hi-lo < 0x800 || hi-lo > 1<<20 {
		return
	}
	vectorBlobLo = lo
	vectorBlobHi = hi
}

// badResumeRIP reports whether next's saved (ELR, SPSR) pair must not be
// ERET'd. The scheduler halts loudly (badResumeHalt) when this returns true.
//
//go:nosplit
func badResumeRIP(next *Thread) bool {
	return proc.BadResumeARM64(next.Context.GetPC(), next.Context.GetProcessorState(), vectorBlobLo, vectorBlobHi)
}
