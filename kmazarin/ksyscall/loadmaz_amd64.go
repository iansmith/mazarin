package ksyscall

// patchJAL_RISCV is a no-op on x86_64.
func patchJAL_RISCV(instrVA, targetAddr uint64, l0PA uintptr) {}

// patchJ_RISCV is a no-op on x86_64.
func patchJ_RISCV(funcVA, targetAddr uint64, l0PA uintptr) {}
