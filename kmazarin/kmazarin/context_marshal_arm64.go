//go:build arm64

package main

// runContextMarshalSelfTest is the arm64 counterpart of the amd64 context-marshal
// boot selftest (MAZ-135). No-op for now: arm64's ThreadContext has a SINGLE g
// home (X28) saved/restored verbatim, so it has no dual-home hazard. Give it the
// same synthetic-input value-flow check over arm64's savers if arm64 ever grows a
// derived saved-context field that a save site could miss.
func runContextMarshalSelfTest() {}
