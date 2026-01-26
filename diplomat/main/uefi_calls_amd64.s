// diplomat/main/uefi_calls_amd64.s
// UEFI call wrappers using Microsoft x64 calling convention
//
// This file works with GOOS=linux GOARCH=amd64 builds.
//
// Microsoft x64 ABI (used by UEFI on x86_64):
//   - Arguments: RCX, RDX, R8, R9, then stack
//   - Return value: RAX
//   - Caller saves: RAX, RCX, RDX, R8, R9, R10, R11
//   - Callee saves: RBX, RBP, RDI, RSI, R12-R15
//   - Stack: 16-byte aligned before CALL, shadow space (32 bytes) required
//
// Go System V ABI on x86_64 (GOOS=linux):
//   - Arguments: on stack at offset(FP)
//   - Return value: stored on stack (if any)
//   - We translate from Go's stack-based args to MS x64 register-based args

#include "textflag.h"

// func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
//
// Calls UEFI OutputString method with Microsoft x64 calling convention:
//   EFI_STATUS OutputString(
//       IN EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL *This,  // RCX
//       IN CHAR16 *String                          // RDX
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
	// Place it after shadow space
	MOVW char+8(FP), AX      // Load the uint16 character
	MOVW AX, 32(SP)          // Store character at SP+32
	MOVW $0, 34(SP)          // Store null terminator at SP+34

	// Load address of the string into RDX (second arg for MS x64 ABI)
	LEAQ 32(SP), DX

	// Get OutputString function pointer from the protocol
	// OutputString is at offset 8 in EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
	// (offset 0 is Reset, offset 8 is OutputString)
	MOVQ 8(CX), AX

	// Call the UEFI function
	// Note: UEFI functions may trash RCX, RDX, R8-R11
	CALL AX

	// Return value (EFI_STATUS) is in RAX
	// We ignore it for this function

	RET

// func uefiCallAllocatePages(funcPtr uintptr, allocType uint32, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS
//
// Calls UEFI AllocatePages with Microsoft x64 calling convention:
//   EFI_STATUS AllocatePages(
//       IN EFI_ALLOCATE_TYPE Type,        // RCX (uint32)
//       IN EFI_MEMORY_TYPE MemoryType,    // RDX (uint32)
//       IN UINTN Pages,                   // R8  (uint64)
//       IN OUT EFI_PHYSICAL_ADDRESS *Memory  // R9 (pointer)
//   );
//
// Go arguments on stack:
//   funcPtr+0(FP)    - 8 bytes
//   allocType+8(FP)  - 4 bytes (uint32)
//   memoryType+12(FP) - 4 bytes (uint32)
//   pages+16(FP)     - 8 bytes (uint64)
//   memory+24(FP)    - 8 bytes (pointer)
//   ret+32(FP)       - 8 bytes (EFI_STATUS return value)
TEXT ·uefiCallAllocatePages(SB), NOSPLIT, $32-40
	// Stack frame: 32 bytes for shadow space
	// Args: 5 parameters = 40 bytes

	// Load arguments into MS x64 ABI registers
	MOVL allocType+8(FP), CX    // RCX = allocType (uint32)
	MOVL memoryType+12(FP), DX  // RDX = memoryType (uint32)
	MOVQ pages+16(FP), R8       // R8 = pages (uint64)
	MOVQ memory+24(FP), R9      // R9 = memory pointer

	// Load function pointer
	MOVQ funcPtr+0(FP), AX

	// Call UEFI function
	CALL AX

	// Return EFI_STATUS (in RAX)
	MOVQ AX, ret+32(FP)
	RET

// func uefiCallFreePages(funcPtr uintptr, memory uint64, pages uint64) EFI_STATUS
//
// Calls UEFI FreePages with Microsoft x64 calling convention:
//   EFI_STATUS FreePages(
//       IN EFI_PHYSICAL_ADDRESS Memory,   // RCX (uint64)
//       IN UINTN Pages                    // RDX (uint64)
//   );
//
// Go arguments on stack:
//   funcPtr+0(FP) - 8 bytes
//   memory+8(FP)  - 8 bytes (uint64)
//   pages+16(FP)  - 8 bytes (uint64)
//   ret+24(FP)    - 8 bytes (EFI_STATUS return value)
TEXT ·uefiCallFreePages(SB), NOSPLIT, $32-32
	// Stack frame: 32 bytes for shadow space
	// Args: 3 parameters = 32 bytes

	// Load arguments into MS x64 ABI registers
	MOVQ memory+8(FP), CX   // RCX = memory (uint64)
	MOVQ pages+16(FP), DX   // RDX = pages (uint64)

	// Load function pointer
	MOVQ funcPtr+0(FP), AX

	// Call UEFI function
	CALL AX

	// Return EFI_STATUS (in RAX)
	MOVQ AX, ret+24(FP)
	RET

// func uefiCallBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize, buffer, funcPtr uintptr) EFI_STATUS
//
// Calls UEFI EFI_BLOCK_IO_PROTOCOL.ReadBlocks with Microsoft x64 calling convention:
//   EFI_STATUS ReadBlocks(
//       IN EFI_BLOCK_IO_PROTOCOL *This,  // RCX
//       IN UINT32 MediaId,               // RDX (uint32, zero-extended)
//       IN EFI_LBA LBA,                  // R8  (uint64)
//       IN UINTN BufferSize,             // R9  (uint64)
//       OUT VOID *Buffer                 // Stack at 32(RSP) after shadow space
//   );
//
// Go arguments on stack (ABI0):
//   protocol+0(FP)   - 8 bytes (uintptr)
//   mediaId+8(FP)    - 4 bytes (uint32), padded to 8
//   lba+16(FP)       - 8 bytes (uint64)
//   bufferSize+24(FP)- 8 bytes (uint64)
//   buffer+32(FP)    - 8 bytes (uintptr)
//   funcPtr+40(FP)   - 8 bytes (uintptr)
//   ret+48(FP)       - 8 bytes (EFI_STATUS)
TEXT ·uefiCallBlockIORead(SB), NOSPLIT, $48-56
	// Stack frame: 48 bytes (32 shadow + 8 for 5th arg + 8 alignment)

	// Load arguments into MS x64 ABI registers
	MOVQ protocol+0(FP), CX     // RCX = This (protocol pointer)
	MOVL mediaId+8(FP), DX      // RDX = MediaId (uint32, zero-extended)
	MOVQ lba+16(FP), R8         // R8 = LBA
	MOVQ bufferSize+24(FP), R9  // R9 = BufferSize

	// 5th argument goes on stack after 32-byte shadow space
	MOVQ buffer+32(FP), AX
	MOVQ AX, 32(SP)             // Buffer at RSP+32

	// Load function pointer and call
	MOVQ funcPtr+40(FP), AX
	CALL AX

	// Return EFI_STATUS
	MOVQ AX, ret+48(FP)
	RET

// func uefiCallBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize, buffer, funcPtr uintptr) EFI_STATUS
//
// Calls UEFI EFI_BLOCK_IO_PROTOCOL.WriteBlocks with Microsoft x64 calling convention:
//   EFI_STATUS WriteBlocks(
//       IN EFI_BLOCK_IO_PROTOCOL *This,  // RCX
//       IN UINT32 MediaId,               // RDX (uint32, zero-extended)
//       IN EFI_LBA LBA,                  // R8  (uint64)
//       IN UINTN BufferSize,             // R9  (uint64)
//       IN VOID *Buffer                  // Stack at 32(RSP) after shadow space
//   );
//
// Go arguments on stack (ABI0):
//   protocol+0(FP)   - 8 bytes (uintptr)
//   mediaId+8(FP)    - 4 bytes (uint32), padded to 8
//   lba+16(FP)       - 8 bytes (uint64)
//   bufferSize+24(FP)- 8 bytes (uint64)
//   buffer+32(FP)    - 8 bytes (uintptr)
//   funcPtr+40(FP)   - 8 bytes (uintptr)
//   ret+48(FP)       - 8 bytes (EFI_STATUS)
TEXT ·uefiCallBlockIOWrite(SB), NOSPLIT, $48-56
	// Stack frame: 48 bytes (32 shadow + 8 for 5th arg + 8 alignment)

	// Load arguments into MS x64 ABI registers
	MOVQ protocol+0(FP), CX     // RCX = This (protocol pointer)
	MOVL mediaId+8(FP), DX      // RDX = MediaId (uint32, zero-extended)
	MOVQ lba+16(FP), R8         // R8 = LBA
	MOVQ bufferSize+24(FP), R9  // R9 = BufferSize

	// 5th argument goes on stack after 32-byte shadow space
	MOVQ buffer+32(FP), AX
	MOVQ AX, 32(SP)             // Buffer at RSP+32

	// Load function pointer and call
	MOVQ funcPtr+40(FP), AX
	CALL AX

	// Return EFI_STATUS
	MOVQ AX, ret+48(FP)
	RET
