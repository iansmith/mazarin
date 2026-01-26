// diplomat/main/entry_amd64.s
// UEFI entry point and minimal Go runtime initialization
//
// UEFI firmware calls efi_main with MS x64 calling convention:
//   EFI_STATUS efi_main(EFI_HANDLE ImageHandle, EFI_SYSTEM_TABLE *SystemTable)
//   - ImageHandle in RCX
//   - SystemTable in RDX
//
// This shim:
//   1. Saves UEFI parameters to globals
//   2. Calls DiplomatEntry directly (minimal runtime)

#include "textflag.h"

// efi_main is the UEFI entry point
// UEFI firmware calls this with MS x64 ABI
// Use WRAPPER flag to force symbol export
TEXT efi_main<>(SB), NOSPLIT|NOFRAME|WRAPPER, $0
	// At entry:
	//   RCX = ImageHandle (EFI_HANDLE)
	//   RDX = SystemTable pointer (EFI_SYSTEM_TABLE*)
	//   Stack provided by UEFI firmware (already 16-byte aligned before call)

	// Save UEFI parameters to globals (use main· prefix for package symbols)
	MOVQ CX, main·imageHandle(SB)
	MOVQ DX, main·systemTable(SB)

	// Initialize g register to nil for now
	// Go runtime will set this up properly when needed
	XORQ R14, R14

	// Call DiplomatEntry directly without full runtime initialization
	// This is like Cardinal's approach - minimal shim
	CALL main·DiplomatEntry(SB)

	// DiplomatEntry should never return, but if it does,
	// return EFI_SUCCESS (0) to UEFI
	XORL AX, AX
	RET

// Export efi_main as a global symbol
GLOBL efi_main<>(SB), RODATA, $8
DATA efi_main<>+0(SB)/8, $efi_main<>(SB)
