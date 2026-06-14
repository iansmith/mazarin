//go:build arm64

package main

// runXMMNestSelfTest is amd64-only. The single-global exception save-state
// clobber (·xmmSaveArea et al.) that MAZ-139 fixes is an amd64 defect; ARM64
// keeps per-context FP/SIMD state in its trap frame on SP_EL1, so it has no
// analogue. No-op here so the shared main.go gate compiles on both arches.
func runXMMNestSelfTest() {}
