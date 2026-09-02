package proc

// BadResumeARM64 is the one definition of the ARM64 scheduler resume-guard
// predicate (host-testable with injected bounds; the kernel's badResumeRIP
// wires in the live exception-vector blob bounds). Extracted verbatim from
// kmazarin's resume_guard_arm64.go (MAZ-196): today the ARM64 guard rejects
// only the impossible PC==0.
//
// pc/spsr are the saved (ELR_EL1, SPSR_EL1) pair about to be ERET'd;
// blobLo/blobHi bound the exception-vector handler blob (blobLo==0 means
// bounds not published — vector checks are skipped).
//
//go:nosplit
func BadResumeARM64(pc, spsr, blobLo, blobHi uint64) bool {
	return pc == 0
}
