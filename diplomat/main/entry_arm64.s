//go:build arm64

// diplomat/main/entry_arm64.s
// UEFI entry point for ARM64 - _efi_main_arm64 is the PE entry point
//
// UEFI firmware calls _efi_main_arm64 with AAPCS64 calling convention:
//   - X0 = ImageHandle
//   - X1 = SystemTable
//
// ARM64 UEFI uses AAPCS64 - the SAME as Go on ARM64!
// This means no ABI translation is needed for UEFI calls.
//
// Boot sequence:
//   1. Save UEFI parameters to globals
//   2. Initialize g0/m0 (minimal Go runtime)
//   3. Set up TLS via TPIDR_EL0
//   4. Call DiplomatEntry (Go function)

#include "textflag.h"

// _efi_main_arm64 is the raw UEFI entry point for ARM64
// This creates symbol main._efi_main_arm64 which elf2pe will find
TEXT main·_efi_main_arm64(SB), NOSPLIT|NOFRAME, $0
	// At entry from UEFI:
	//   X0 = ImageHandle (EFI_HANDLE)
	//   X1 = SystemTable pointer (EFI_SYSTEM_TABLE*)
	//   Stack provided by UEFI firmware

	// ========================================
	// Step 1: Save UEFI Parameters
	// ========================================
	// UEFI passes ImageHandle in R0, SystemTable in R1
	MOVD	R0, main·imageHandle(SB)
	MOVD	R1, main·systemTable(SB)

	// ========================================
	// Step 2: Initialize g0/m0 (Minimal Go Runtime)
	// ========================================
	// Set g register (R28 on ARM64) to point to runtime.g0
	MOVD	$runtime·g0(SB), g	// g = R28

	// Set up stack guards for g0
	// Stack guard = current SP - 64KB safety margin
	MOVD	RSP, R12		// Save current stack pointer
	MOVD	RSP, R0
	SUB	$65536, R0, R0		// Stack guard = SP - 64KB

	// g.stackguard0 and g.stackguard1 (offsets 16, 24)
	MOVD	R0, 16(g)		// g0.stackguard0
	MOVD	R0, 24(g)		// g0.stackguard1

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOVD	R0, 0(g)		// g0.stack.lo
	MOVD	R12, 8(g)		// g0.stack.hi

	// Link g0 and m0
	MOVD	$runtime·m0(SB), R0
	MOVD	R0, 48(g)		// g0.m = &m0 (offset 48)
	MOVD	g, (R0)			// m0.g0 = &g0 (offset 0)

	// ========================================
	// Step 3: Set up TLS
	// ========================================
	// Go's ARM64 TLS uses TPIDR_EL0
	// The runtime expects g at a negative offset from TPIDR_EL0
	// Store g0 at tlsBlock[0], set TPIDR_EL0 = tlsBlock + 8
	// Then loading from [TPIDR_EL0, #-8] gives g

	// Store g0 address at offset 0 in TLS block
	MOVD	$main·tlsBlock(SB), R0
	MOVD	g, (R0)			// Store g0 address at tlsBlock[0]

	// Set TPIDR_EL0 = tlsBlock + 8
	ADD	$8, R0, R0
	MSR	R0, TPIDR_EL0

	// ========================================
	// Step 4: Call DiplomatEntry via TOPFRAME wrapper
	// ========================================
	// Now Go functions can run - g register is set, stack guards initialized, TLS set up.
	// We must save UEFI's return address since BL overwrites LR.
	MOVD	R30, main·uefiReturnAddr(SB)
	BL	main·diplomatTopFrame(SB)

	// DiplomatEntry should never return, but if it does,
	// return EFI_SUCCESS (0) to UEFI
	MOVD	main·uefiReturnAddr(SB), R30
	MOVD	$0, R0
	RET

// diplomatTopFrame is marked TOPFRAME so Go's stack unwinder stops here.
// Without this, the unwinder walks past _efi_main_arm64 into UEFI's stack
// and panics on the unrecognized return address.
TEXT main·diplomatTopFrame(SB), NOSPLIT|TOPFRAME, $8
	MOVD	R30, (RSP)
	BL	main·DiplomatEntry(SB)
	MOVD	(RSP), R30
	RET

// _minimal_uefi_test_arm64 is a minimal entry point for toolchain validation
// It does the bare minimum to prove UEFI called us
TEXT main·_minimal_uefi_test_arm64(SB), NOSPLIT|NOFRAME, $0
	// Just save parameters and return success
	// UEFI passes ImageHandle in R0, SystemTable in R1
	MOVD	R0, main·imageHandle(SB)
	MOVD	R1, main·systemTable(SB)
	MOVD	$1, R0
	MOVD	R0, main·executionMarker(SB)
	MOVD	$0, R0	// EFI_SUCCESS
	RET
