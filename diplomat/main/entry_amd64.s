// diplomat/main/entry_amd64.s
// UEFI entry point - efi_main is the PE entry point set by elf2pe
//
// UEFI firmware calls efi_main with MS x64 calling convention:
//   - ImageHandle in RCX
//   - SystemTable in RDX
//
// Boot sequence (following Cardinal's pattern):
//   1. Save UEFI parameters to globals
//   2. Initialize g0/m0 (minimal Go runtime)
//   3. Call DiplomatEntry (Go function)

#include "textflag.h"

// _efi_main_asm is the raw UEFI entry point (no Go declaration, no .abi0 wrapper)
// This creates symbol main._efi_main_asm which doesn't match any Go function,
// so no ABI wrapper is generated. This is critical - wrappers try to load g from TLS
// before we've set it up.
TEXT main·_efi_main_asm(SB), NOSPLIT|NOFRAME, $0
	// At entry from UEFI:
	//   RCX = ImageHandle (EFI_HANDLE)
	//   RDX = SystemTable pointer (EFI_SYSTEM_TABLE*)
	//   Stack provided by UEFI firmware (already 16-byte aligned before call)

	// ========================================
	// Step 1: Save UEFI Parameters FIRST (before any debug output clobbers them!)
	// ========================================
	MOVQ CX, main·imageHandle(SB)
	MOVQ DX, main·systemTable(SB)

	// DEBUG: Output 'A' to QEMU debug port 0xE9 - entry reached
	// Safe to clobber DX now since we've saved SystemTable
	MOVB $'A', AL
	MOVW $0xE9, DX
	OUTB

	// DEBUG: Output 'B' - parameters saved
	MOVB $'B', AL
	MOVW $0xE9, DX
	OUTB

	// ========================================
	// Step 2: Initialize g0/m0 (Minimal Go Runtime)
	// ========================================
	// Following Cardinal's Segment 13 pattern

	// Set g register (R14 on x86_64) to point to runtime.g0
	MOVQ $runtime·g0(SB), R14

	// Set up stack guards for g0
	// Stack guard = current SP - 64KB safety margin
	MOVQ SP, R12			// Save current stack pointer
	MOVQ SP, AX
	SUBQ $(64*1024), AX		// Stack guard = SP - 64KB

	// g.stackguard0 and g.stackguard1 (offsets 16, 24)
	MOVQ AX, 16(R14)		// g0.stackguard0
	MOVQ AX, 24(R14)		// g0.stackguard1

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOVQ AX, 0(R14)			// g0.stack.lo
	MOVQ R12, 8(R14)		// g0.stack.hi

	// Link g0 and m0
	MOVQ $runtime·m0(SB), AX
	MOVQ AX, 48(R14)		// g0.m = &m0 (offset 48)
	MOVQ R14, (AX)			// m0.g0 = &g0 (offset 0)

	// DEBUG: Output 'C' - g0/m0 initialized
	MOVB $'C', AL
	MOVW $0xE9, DX
	OUTB

	// ========================================
	// Step 2.5: Set up TLS (CRITICAL for .abi0 wrappers)
	// ========================================
	// Go's .abi0 wrappers load g from %fs:-0x8
	// So if we want fs:-8 to read g0, we need:
	//   - Store g0 at tlsBlock[0]
	//   - Set FS = tlsBlock + 8
	// Then fs:-8 = (tlsBlock + 8) - 8 = tlsBlock[0] = g0 ✓

	// Store g0 address at offset 0 in TLS block
	MOVQ $main·tlsBlock(SB), AX	// Get TLS block address
	MOVQ R14, (AX)			// Store g0 address at tlsBlock[0]

	// DEBUG: Output 'D' - TLS block address stored
	MOVB $'D', AL
	MOVW $0xE9, DX
	OUTB

	// Set FS segment base to tlsBlock + 8
	// This way fs:-8 reads from tlsBlock[0] where g0 is stored
	MOVQ $main·tlsBlock(SB), AX
	ADDQ $8, AX			// AX = tlsBlock + 8

	// Set FS segment base using WRMSR (MSR_FS_BASE = 0xC0000100)
	// WRMSR writes EDX:EAX to MSR[ECX]
	MOVQ AX, DX
	SHRQ $32, DX			// DX = upper 32 bits
	// AX already has lower 32 bits
	MOVL $0xC0000100, CX		// MSR_FS_BASE
	BYTE $0x0F			// WRMSR instruction
	BYTE $0x30

	// DEBUG: Output 'E' - TLS set up complete
	MOVB $'E', AL
	MOVW $0xE9, DX
	OUTB

	// ========================================
	// Step 3: Call DiplomatEntry
	// ========================================
	// Now Go functions can run - g register is set, stack guards initialized, TLS set up

	// DEBUG: Output 'F' - about to call DiplomatEntry
	MOVB $'F', AL
	MOVW $0xE9, DX
	OUTB

	CALL main·DiplomatEntry(SB)

	// DEBUG: Output 'X' - returned from DiplomatEntry (should never happen)
	MOVB $'X', AL
	MOVW $0xE9, DX
	OUTB

	// DiplomatEntry should never return, but if it does,
	// return EFI_SUCCESS (0) to UEFI
	XORL AX, AX
	RET
