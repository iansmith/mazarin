// diplomat/main/minimal_test_amd64.s
// Minimal UEFI test - pure assembly, no Go runtime needed
//
// This file proves that:
// 1. The PE loads correctly
// 2. UEFI calls our entry point
// 3. We can call UEFI ConOut->OutputString
//
// UEFI MS x64 calling convention:
//   RCX = 1st arg, RDX = 2nd arg, R8 = 3rd arg, R9 = 4th arg
//   Return value in RAX
//   Caller must reserve 32 bytes shadow space on stack

#include "textflag.h"

// EFI_SYSTEM_TABLE offsets (x86_64):
//   +0:   Hdr (24 bytes)
//   +24:  FirmwareVendor (8 bytes ptr)
//   +32:  FirmwareRevision (4 bytes)
//   +36:  padding (4 bytes)
//   +40:  ConsoleInHandle (8 bytes)
//   +48:  ConIn (8 bytes ptr)
//   +56:  ConsoleOutHandle (8 bytes)
//   +64:  ConOut (8 bytes ptr) ← WE NEED THIS

// EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL offsets:
//   +0:   Reset (8 bytes ptr)
//   +8:   OutputString (8 bytes ptr) ← WE NEED THIS

// _minimal_uefi_test is a pure assembly entry point
TEXT main·_minimal_uefi_test(SB), NOSPLIT|NOFRAME, $0
	// UEFI calls us with:
	//   RCX = ImageHandle
	//   RDX = SystemTable pointer

	// Save SystemTable immediately (before we clobber DX for debug)
	MOVQ DX, R8         // R8 = SystemTable (temporary save)

	// DEBUG: Output '1' to QEMU debug port 0xE9 - entry reached
	MOVB $'1', AL
	MOVW $0xE9, DX
	OUTB

	// Save callee-saved registers we'll use
	PUSHQ BP
	MOVQ SP, BP
	PUSHQ BX
	PUSHQ R12
	PUSHQ R13

	// Restore SystemTable from R8
	MOVQ R8, R12        // R12 = SystemTable

	// DEBUG: Output '2' - saved registers
	MOVB $'2', AL
	MOVW $0xE9, DX
	OUTB

	// Get ConOut from SystemTable (offset 64 = 0x40)
	MOVQ 64(R12), R13   // R13 = ConOut

	// DEBUG: Output '3' - loaded ConOut pointer
	MOVB $'3', AL
	MOVW $0xE9, DX
	OUTB

	// Check if ConOut is null
	TESTQ R13, R13
	JZ exit_null_conout

	// DEBUG: Output '4' - ConOut not null
	MOVB $'4', AL
	MOVW $0xE9, DX
	OUTB

	// Get OutputString function pointer (offset 8 in ConOut)
	MOVQ 8(R13), BX     // BX = OutputString function

	// Check if OutputString is null
	TESTQ BX, BX
	JZ exit_null_func

	// DEBUG: Output '5' - OutputString not null
	MOVB $'5', AL
	MOVW $0xE9, DX
	OUTB

	// Allocate shadow space (32 bytes) + space for string
	SUBQ $64, SP

	// Build a simple UCS-2 string on the stack: "OK\r\n\0"
	// UCS-2 is little-endian 16-bit characters
	MOVW $'O', 32(SP)
	MOVW $'K', 34(SP)
	MOVW $'\r', 36(SP)
	MOVW $'\n', 38(SP)
	MOVW $0, 40(SP)     // null terminator

	// DEBUG: Output '6' - about to call OutputString
	MOVB $'6', AL
	MOVW $0xE9, DX
	OUTB

	// Call OutputString(ConOut, String)
	// MS x64: RCX = this (ConOut), RDX = String
	MOVQ R13, CX        // CX = ConOut (this pointer)
	LEAQ 32(SP), DX     // DX = pointer to string
	CALL BX             // Call OutputString

	// DEBUG: Output '7' - returned from OutputString
	MOVB $'7', AL
	MOVW $0xE9, DX
	OUTB

	// Clean up stack
	ADDQ $64, SP

	// Return EFI_SUCCESS (0)
	XORL AX, AX
	JMP exit_done

exit_null_conout:
	// DEBUG: Output 'N' - ConOut was null
	MOVB $'N', AL
	MOVW $0xE9, DX
	OUTB
	MOVL $7, AX         // EFI_DEVICE_ERROR
	JMP exit_done

exit_null_func:
	// DEBUG: Output 'F' - OutputString was null
	MOVB $'F', AL
	MOVW $0xE9, DX
	OUTB
	MOVL $7, AX         // EFI_DEVICE_ERROR
	JMP exit_done

exit_done:
	// DEBUG: Output 'X' - exiting
	MOVB $'X', AL
	MOVW $0xE9, DX
	OUTB

	// Restore callee-saved registers
	POPQ R13
	POPQ R12
	POPQ BX
	POPQ BP
	RET
