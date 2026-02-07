//go:build amd64

package main

// computeHWCAP returns 0 on x86_64 — HWCAP is ARM64-specific.
func computeHWCAP() uint64 { return 0 }
