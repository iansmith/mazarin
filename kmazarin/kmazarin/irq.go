package main

import "mazzy/kmazarin/ksync"

// SaveAndDisableIRQs and RestoreIRQs are thin wrappers over the kernel's single
// canonical IRQ save/restore implementation in ksync (MAZ-167 — the former
// package-main asm copy was one of the duplicates that carried the MAZ-128
// NZCV-for-DAIF encoding typo). Kept under their established names so the
// package's many call sites and the ksyscall go:linkname bridge are unchanged.

// SaveAndDisableIRQs saves the current interrupt state and disables IRQs.
// Returns the saved state which should be passed to RestoreIRQs.
// This allows nested disable/restore pairs.
//
//go:nosplit
func SaveAndDisableIRQs() uint64 {
	return ksync.SaveAndDisableIRQs()
}

// RestoreIRQs restores the interrupt state to a previously saved value.
// Use with SaveAndDisableIRQs for nested critical sections.
//
//go:nosplit
func RestoreIRQs(savedDAIF uint64) {
	ksync.RestoreIRQs(savedDAIF)
}
