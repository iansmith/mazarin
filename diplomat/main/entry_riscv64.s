//go:build riscv64

// diplomat/main/entry_riscv64.s
// UEFI entry point for RISC-V 64-bit - _efi_main_riscv64 is the PE entry point
//
// UEFI firmware calls _efi_main_riscv64 with standard RISC-V calling convention:
//   - A0 = ImageHandle
//   - A1 = SystemTable
//
// RISC-V UEFI uses the standard RISC-V calling convention - the SAME as Go on RISC-V!
// This means no ABI translation is needed for UEFI calls (like ARM64).
//
// Boot sequence:
//   1. Save UEFI parameters to globals
//   2. Initialize g0/m0 (minimal Go runtime)
//   3. Set up TLS via TP register
//   4. Call DiplomatEntry (Go function)

#include "textflag.h"

// _efi_main_riscv64 is the raw UEFI entry point for RISC-V 64-bit
// This creates symbol main._efi_main_riscv64 which elf2pe will find
TEXT main·_efi_main_riscv64(SB), NOSPLIT|NOFRAME, $0
	// At entry from UEFI:
	//   A0 = ImageHandle (EFI_HANDLE)
	//   A1 = SystemTable pointer (EFI_SYSTEM_TABLE*)
	//   Stack provided by UEFI firmware

	// ========================================
	// Step 1: Save UEFI Parameters
	// ========================================
	// UEFI passes ImageHandle in A0, SystemTable in A1
	MOV	A0, T0
	MOV	$main·imageHandle(SB), T1
	MOV	T0, (T1)

	MOV	A1, T0
	MOV	$main·systemTable(SB), T1
	MOV	T0, (T1)

	// ========================================
	// Step 2: Initialize g0/m0 (Minimal Go Runtime)
	// ========================================
	// Set g register (S10/X26 on RISC-V) to point to runtime.g0
	MOV	$runtime·g0(SB), g	// g = X26 (S10)

	// Set up stack guards for g0
	// Stack guard = current SP - 64KB safety margin
	MOV	X2, T0			// Save current stack pointer (X2 = SP)
	MOV	X2, T1
	ADD	$-65536, T1		// Stack guard = SP - 64KB

	// g.stackguard0 and g.stackguard1 (offsets 16, 24)
	MOV	T1, 16(g)		// g0.stackguard0
	MOV	T1, 24(g)		// g0.stackguard1

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOV	T1, 0(g)		// g0.stack.lo
	MOV	T0, 8(g)		// g0.stack.hi

	// Link g0 and m0
	MOV	$runtime·m0(SB), T0
	MOV	T0, 48(g)		// g0.m = &m0 (offset 48)
	MOV	g, (T0)			// m0.g0 = &g0 (offset 0)

	// ========================================
	// Step 3: Set up TLS
	// ========================================
	// Go's RISC-V TLS uses the TP register (X4)
	// The runtime expects g at a negative offset from TP
	// Store g0 at tlsBlock[0], set TP = tlsBlock + 8
	// Then loading from [TP, #-8] gives g

	// Store g0 address at offset 0 in TLS block
	MOV	$main·tlsBlock(SB), T0
	MOV	g, (T0)			// Store g0 address at tlsBlock[0]

	// Set TP = tlsBlock + 8
	ADD	$8, T0, TP		// TP = X4

	// ========================================
	// Step 4: Call DiplomatEntry
	// ========================================
	// Now Go functions can run - g register is set, stack guards initialized, TLS set up
	CALL	main·DiplomatEntry(SB)

	// DiplomatEntry should never return, but if it does,
	// return EFI_SUCCESS (0) to UEFI
	MOV	$0, A0
	RET

// _minimal_uefi_test_riscv64 is a minimal entry point for toolchain validation
// It does the bare minimum to prove UEFI called us
TEXT main·_minimal_uefi_test_riscv64(SB), NOSPLIT|NOFRAME, $0
	// Just save parameters and return success
	// UEFI passes ImageHandle in A0, SystemTable in A1
	MOV	A0, T0
	MOV	$main·imageHandle(SB), T1
	MOV	T0, (T1)

	MOV	A1, T0
	MOV	$main·systemTable(SB), T1
	MOV	T0, (T1)

	MOV	$1, T0
	MOV	$main·executionMarker(SB), T1
	MOV	T0, (T1)

	MOV	$0, A0		// EFI_SUCCESS
	RET
