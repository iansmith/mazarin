package proc

// BadResumeARM64 is the one definition of the ARM64 scheduler resume-guard
// predicate (host-testable with injected bounds; the kernel's badResumeRIP
// wires in the live exception-vector blob bounds).
//
// pc/spsr are the saved (ELR_EL1, SPSR_EL1) pair about to be ERET'd;
// blobLo/blobHi bound the exception-vector handler blob (blobLo==0 means
// bounds not published — vector checks are skipped).
//
// Two rejections (MAZ-196):
//   - pc == 0: the pre-existing impossible-value check.
//   - poisoned pair: handler-blob code only ever executes at EL1h, so a PC
//     inside the blob paired with SPSR mode bits != EL1h (spsr&0xF != 5) is
//     poisoned by definition — exactly what the MAZ-193 SPSR hardcode
//     produced. ERETing such a context runs handler code in the wrong mode
//     on the wrong stack; halt loudly before the ERET instead.
//
// SPSRModeEL1h is the SPSR_EL1 M[3:0] encoding for EL1 with SP_EL1 (EL1h) —
// the only mode exception-handler code ever executes in.
const SPSRModeEL1h = 0x5

//go:nosplit
func BadResumeARM64(pc, spsr, blobLo, blobHi uint64) bool {
	if pc == 0 {
		return true
	}
	return blobLo != 0 && pc >= blobLo && pc < blobHi && spsr&0xF != SPSRModeEL1h
}
