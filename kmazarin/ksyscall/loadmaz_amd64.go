package ksyscall

// patchJAL_RISCV is a no-op on x86_64. JAL_RISCV reloc records never
// appear in x86_64 .maz binaries (they use CALL_X86 instead).
func patchJAL_RISCV(instrVA, targetAddr uint64, l0PA uintptr) {}
