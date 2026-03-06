package ksyscall

// patchJAL_RISCV is a no-op on ARM64. JAL_RISCV reloc records never
// appear in ARM64 .maz binaries (they use BL_ARM64 instead).
func patchJAL_RISCV(instrVA, targetAddr uint64, l0PA uintptr) {}
