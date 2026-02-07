//go:build amd64

package main

// buildSyntheticDTB returns 0 on x86_64 — DTB is ARM64-specific.
func buildSyntheticDTB(hw *HardwareInfo) uint64 { return 0 }
