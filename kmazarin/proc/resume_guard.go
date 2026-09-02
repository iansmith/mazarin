package proc

// SPSRModeEL1h is the SPSR_EL1 M[4:0] encoding for AArch64 EL1 with SP_EL1
// (EL1h) — the only mode exception-handler code ever executes in. M[4]=0
// (AArch64) is part of the encoding: an AArch32 mode must not pass.
const SPSRModeEL1h = 0x5

// spsrModeMask covers M[4:0] — the mode bits plus the AArch32 state bit.
const spsrModeMask = 0x1F

// BadResumeARM64 is the one definition of the ARM64 scheduler resume-guard
// predicate (host-testable with injected bounds; the kernel's badResumeRIP
// wires in the live exception-vector blob bounds).
//
// pc/spsr are the saved (ELR_EL1, SPSR_EL1) pair about to be resumed;
// blobLo/blobHi bound the exception-vector handler blob (blobLo==0 means
// bounds not published — the vector check is skipped).
//
// Two rejections (MAZ-196):
//   - pc == 0: the pre-existing impossible-value check.
//   - poisoned pair: handler-blob code only ever executes at EL1h, so a PC
//     inside the blob paired with SPSR mode != EL1h is poisoned by
//     definition. Scope, honestly: this guard runs at the Go-side scheduler
//     resume sites, which the MAZ-179 probe record (tier-17/18) proved the
//     MAZ-193 poison BYPASSED — that pair reached ERET via asm
//     exception-return paths that never consult badResumeRIP. The producer
//     bug is fixed (MAZ-193); this predicate is defence-in-depth for any
//     future producer that routes a poisoned context through the scheduler.
//     The consumption-waist (asm pre-ERET) guard is tracked separately.
//
//go:nosplit
func BadResumeARM64(pc, spsr, blobLo, blobHi uint64) bool {
	if pc == 0 {
		return true
	}
	return blobLo != 0 && pc >= blobLo && pc < blobHi && spsr&spsrModeMask != SPSRModeEL1h
}
