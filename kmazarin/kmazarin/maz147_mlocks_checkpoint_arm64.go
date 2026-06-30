//go:build arm64

package main

import "unsafe"

// MAZ-147 arm64-specific half: the preempt SKIP-guard `g0PreemptHoldsMLocks`. The
// arch-neutral save/globals/selftest are in maz147_mlocks_checkpoint.go; the arm64
// asm RESTORE is `mlockRearmFromFrame` in exceptions_arm64.s, BL'd from each
// `CTX_RESTORE_TO_FRAME` site (keyed on the restored `frame[X28] == kmazarinG0Addr`,
// re-arming via the precomputed `savedG0MLocksPtr`).

// Exception-frame byte offsets — mirror the asm #defines in exceptions_arm64.s
// (the EXC_FRAME_* block near the top of the file). framePtr passed to the guard is
// the live EL1 exception frame base (SP), per checkThreadPreemptionImpl's call sites
// (timer / priority-wake), which pass RSP.
const (
	excFrameX28Off  = 224 // EXC_FRAME_X28 — interrupted X28 (the g register)
	excFrameSPSROff = 264 // EXC_FRAME_ELR_SPSR + 8 — saved SPSR_EL1
)

// g0PreemptHoldsMLocks reports whether an INVOLUNTARY timer preemption is about to
// switch g0 out while it holds m.locks — in which case checkThreadPreemptionImpl
// must SKIP the switch (return 0), mirroring stock Go's "m.locks ⇒ non-preemptible".
// See the amd64 twin for the full why (design §8c). The call site in
// checkThreadPreemptionImpl is arch-neutral; only this frame read is arch-specific.
//
// ARM64 is SIMPLER than amd64: single g-home (X28) means the interrupted g is read
// directly from the frame — no amd64-style gLooksValid(slot)?slot:R14 dance. Kernel
// mode is the saved SPSR.M[2] (EL1) bit, mirroring the IRQ handler's own EL check.
// Dereferences only the frame (always mapped) + g0 — never an arbitrary g.
//
//go:nosplit
func g0PreemptHoldsMLocks(framePtr uintptr) bool {
	if framePtr == 0 {
		return false
	}
	// Kernel-mode (EL1) test: SPSR.M[2] set ⇒ the interrupt was taken from EL1. EL0
	// (userspace) frames are never the kernel g0.
	spsr := *(*uint64)(unsafe.Pointer(framePtr + excFrameSPSROff))
	if spsr&0x4 == 0 {
		return false // taken from EL0 — not the kernel g0
	}
	effG := *(*uint64)(unsafe.Pointer(framePtr + excFrameX28Off))
	if effG != kmazarinG0Addr {
		return false // not running g0 — its own M, not the borrowed m0
	}
	lp := g0MLocksPtr()
	if lp == nil {
		return false
	}
	return *lp > 0
}
