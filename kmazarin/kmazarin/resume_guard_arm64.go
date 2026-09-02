//go:build arm64

package main

import "mazzy/kmazarin/proc"

// badResumeRIP preserves the prior RIP==0 guard on ARM64.
//
// ARM64 keeps g in a single home (X28), saved and restored verbatim in the trap
// frame, so it has no dual-home save gap and has never exhibited the MAZ-136
// kernel-mode resume corruption. The stronger kernel-.text bound lives in the
// amd64 build (resume_guard_amd64.go); here the original cheap check is enough.
//
//go:nosplit
func badResumeRIP(next *Thread) bool {
	return proc.BadResumeARM64(next.Context.GetPC(), next.Context.SPSR, 0, 0)
}

// initResumeGuardBounds is a no-op on ARM64: the asm-level IRETQ guards that
// consume the published bounds are amd64-only (see resume_guard_amd64.go).
//
//go:nosplit
func initResumeGuardBounds() {}
