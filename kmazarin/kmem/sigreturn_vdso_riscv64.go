//go:build riscv64

package kmem

import (
	"mazzy/shared/constants"
	"unsafe"
)

// SigreturnVDSOVA is the fixed user-accessible virtual address of the sigreturn
// vDSO page. This page contains the EBREAK instruction sequence for rt_sigreturn,
// mapped with U+R+X in each priest's page table.
//
// On RISC-V, U-mode cannot execute kernel pages (PTE.U=0). Unlike ARM64 (which
// allows EL0 to fetch instructions from TTBR1 pages without UXN), RISC-V uses a
// single SATP and enforces U-bit checks. So we must map the sigreturn trampoline
// at a user-accessible address, similar to Linux's vDSO.
//
// Address chosen: top of L2[0] range (0-1GB), well above typical ELF load addresses.
const SigreturnVDSOVA uintptr = 0x3FFFF000

// SigreturnVDSOPA is the physical address of the allocated vDSO page.
// Set by InitSigreturnVDSO, read by mapSigreturnVDSOInProcessL0.
var SigreturnVDSOPA uintptr

// InitSigreturnVDSO allocates a physical page, writes the RISC-V sigreturn
// trampoline instructions into it, and stores the PA for later mapping.
// Must be called before any process page tables are created.
//
// The trampoline code is:
//
//	li a7, 139        // SYS_rt_sigreturn
//	ebreak            // trap to kernel
func InitSigreturnVDSO() {
	pa := AllocPage(PageVDSO, 0) // Kernel-allocated, user-accessible trampoline
	if pa == 0 {
		return
	}
	va := pa + constants.KernelMMIOOffset

	// Zero the page
	zeroPageSlow(va)

	// Write the two RISC-V instructions:
	//   li a7, 139  =  addi x17, x0, 139  =  0x08B00893
	//   ebreak      =                         0x00100073
	*(*uint32)(unsafe.Pointer(va)) = 0x08B00893
	*(*uint32)(unsafe.Pointer(va + 4)) = 0x00100073

	// Ensure data cache is flushed and instruction cache sees the new code
	CleanPageCache(va)
	dsbSY()  // FENCE rw,rw
	isbSY()  // FENCE.I — flush instruction cache

	SigreturnVDSOPA = pa
}

// mapSigreturnVDSOInProcessL0 maps the sigreturn vDSO page into a process's
// page table at SigreturnVDSOVA with User+Read+eXecute permissions.
// Called from CreateProcessPageTable after initProcessL0.
func mapSigreturnVDSOInProcessL0(l0PA uintptr) {
	if SigreturnVDSOPA == 0 {
		return // vDSO not initialized yet
	}

	// Map the vDSO page with R+X (read + execute, no write)
	elfFlags := uint32(ELF_PF_R | ELF_PF_X)
	mapUserPageWithL0(SigreturnVDSOVA, SigreturnVDSOPA, elfFlags, l0PA)
}
