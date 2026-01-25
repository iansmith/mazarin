// diplomat/asm/x64/uefi_calls.s
// UEFI call wrappers using Microsoft x64 calling convention
//
// Microsoft x64 ABI (used by UEFI on x86_64):
//   - Arguments: RCX, RDX, R8, R9, then stack (right-to-left)
//   - Return value: RAX
//   - Caller saves: RAX, RCX, RDX, R8, R9, R10, R11
//   - Callee saves: RBX, RBP, RDI, RSI, R12-R15
//   - Stack: 16-byte aligned before CALL, shadow space (32 bytes) required
//
// Go's System V ABI on x86_64:
//   - Arguments: on stack at offset(FP)
//   - Return value: stored on stack
//   - We must translate between the two conventions

#include "textflag.h"

// func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
//
// This function calls the UEFI OutputString method, which has the signature:
//   EFI_STATUS OutputString(
//       IN EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL *This,
//       IN CHAR16 *String
//   );
//
// Go passes arguments on stack:
//   conout at 0(FP)  - 8 bytes (pointer)
//   char at 8(FP)    - 2 bytes (uint16), but Go aligns to 8 bytes
TEXT ·ueficall_OutputString(SB), NOSPLIT, $48-16
	// Stack frame: 48 bytes
	//   - 32 bytes: shadow space (required by MS x64 ABI)
	//   - 16 bytes: space for our UCS-2 string (char + null terminator + padding)
	// Frame size annotation: $48-16 means 48 byte locals, 16 byte args

	// Load conout pointer into RCX (first arg for MS x64 ABI)
	MOVQ conout+0(FP), CX

	// Create UCS-2 string on stack: {char, 0}
	// Place it in the upper part of our frame, after shadow space
	MOVW char+8(FP), AX      // Load the uint16 character
	MOVW AX, 32(SP)          // Store character at SP+32
	MOVW $0, 34(SP)          // Store null terminator at SP+34

	// Load address of the string into RDX (second arg for MS x64 ABI)
	LEAQ 32(SP), DX

	// Get OutputString function pointer from the protocol
	// OutputString is at offset 8 in EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
	// (offset 0 is Reset, offset 8 is OutputString)
	MOVQ 8(CX), AX

	// The stack is already 16-byte aligned (Go guarantees this)
	// Shadow space (32 bytes) is already allocated in our $48 frame

	// Call the UEFI function
	// Note: UEFI functions may trash RCX, RDX, R8-R11
	CALL AX

	// Return value (EFI_STATUS) is in RAX
	// Go doesn't expect a return value for this function, so we ignore it

	RET
