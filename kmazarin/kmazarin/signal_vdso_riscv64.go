//go:build riscv64

package main

import "mazzy/kmazarin/kmem"

// initSigreturnVDSO allocates the user-accessible sigreturn trampoline page
// on RISC-V and updates sigreturnTrampolinePC to use the user VA.
//
// On RISC-V, U-mode cannot execute kernel pages (PTE.U=0), so the sigreturn
// trampoline must be mapped at a user-accessible address. This is analogous
// to Linux's vDSO mechanism.
func initSigreturnVDSO() {
	kmem.InitSigreturnVDSO()
	if kmem.SigreturnVDSOPA != 0 {
		sigreturnTrampolinePC = kmem.SigreturnVDSOVA
	}
}
