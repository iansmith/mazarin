//go:build arm64

package main

// MAZ-141 — intentionally NO priority-wake event ring on ARM64.
//
// ARM64 performs the priority-wake fast switch inline in exceptions_arm64.s with
// a single g home (X28); it has no dual-home `morestack on g0` hazard, so there
// is nothing for a pwake context ring to catch. Switch counts are tracked via
// dbgPWakeSwitched (bottom_half.go). The amd64-only ring + shepherd-exit dump
// live in pwake_trace_amd64.go; ksyscall.OnShepherdExit simply stays nil here.
//
// This per-arch split is inherent to the existing priority-wake switch
// implementation (Go-routed on amd64, inline-asm on ARM64), documented here so
// it is not silent — see feedback_amd64_arch_divergence.md.
