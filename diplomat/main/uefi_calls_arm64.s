//go:build arm64

// diplomat/main/uefi_calls_arm64.s
// UEFI function call wrappers for ARM64
//
// ARM64 UEFI uses AAPCS64 calling convention - the SAME as Go on ARM64!
// This means wrappers are much simpler than x86_64 (no ABI translation needed).
//
// AAPCS64:
//   - Arguments: X0-X7 (first 8 arguments)
//   - Return: X0 (and X1 for 128-bit returns)
//   - Callee-saved: X19-X28, SP
//   - Caller-saved: X0-X18, X29 (FP), X30 (LR)

#include "textflag.h"

// ueficall_OutputString calls UEFI ConOut->OutputString
// Go: func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
// UEFI: EFI_STATUS OutputString(EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL *This, CHAR16 *String)
//
// We need to build a null-terminated UCS-2 string on the stack
TEXT ·ueficall_OutputString(SB), NOSPLIT, $32-16
	MOVD	conout+0(FP), R0	// This (protocol pointer)

	// Build UCS-2 string on stack: [char, 0x0000]
	MOVHU	char+8(FP), R1
	MOVHU	R1, -16(RSP)		// char at SP-16
	MOVHU	ZR, -14(RSP)		// null terminator at SP-14

	// String pointer = SP-16
	SUB	$16, RSP, R1

	// Load OutputString function pointer (offset 8 in protocol)
	MOVD	8(R0), R2
	CALL	(R2)			// Call OutputString(This, String)

	// Return value in R0, but we don't return it (void function in Go)
	RET

// uefiAllocatePages calls UEFI BootServices->AllocatePages
// Go: func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS
// UEFI: EFI_STATUS AllocatePages(EFI_ALLOCATE_TYPE Type, EFI_MEMORY_TYPE MemoryType,
//                                 UINTN Pages, EFI_PHYSICAL_ADDRESS *Memory)
TEXT ·uefiAllocatePages(SB), NOSPLIT, $0-40
	MOVW	allocType+0(FP), R0	// Type
	MOVW	memType+4(FP), R1	// MemoryType
	MOVD	pages+8(FP), R2		// Pages
	MOVD	memory+16(FP), R3	// Memory (out pointer)

	// Get BootServices->AllocatePages
	// systemTable offset 96 = BootServices pointer
	// BootServices offset 40 = AllocatePages
	MOVD	main·systemTable(SB), R4
	MOVD	96(R4), R4		// BootServices
	MOVD	40(R4), R4		// AllocatePages
	CALL	(R4)

	MOVD	R0, ret+24(FP)
	RET

// uefiFreePages calls UEFI BootServices->FreePages
// Go: func uefiFreePages(memory uint64, pages uint64) EFI_STATUS
// UEFI: EFI_STATUS FreePages(EFI_PHYSICAL_ADDRESS Memory, UINTN Pages)
TEXT ·uefiFreePages(SB), NOSPLIT, $0-24
	MOVD	memory+0(FP), R0	// Memory
	MOVD	pages+8(FP), R1		// Pages

	// Get BootServices->FreePages (offset 48)
	MOVD	main·systemTable(SB), R4
	MOVD	96(R4), R4		// BootServices
	MOVD	48(R4), R4		// FreePages
	CALL	(R4)

	MOVD	R0, ret+16(FP)
	RET

// uefiHandleProtocol calls UEFI BootServices->HandleProtocol
// Go: func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS
// UEFI: EFI_STATUS HandleProtocol(EFI_HANDLE Handle, EFI_GUID *Protocol, VOID **Interface)
TEXT ·uefiHandleProtocol(SB), NOSPLIT, $0-32
	MOVD	handle+0(FP), R0	// Handle
	MOVD	protocol+8(FP), R1	// Protocol GUID
	MOVD	iface+16(FP), R2	// Interface (out pointer)

	// Get BootServices->HandleProtocol (offset 152)
	MOVD	main·systemTable(SB), R4
	MOVD	96(R4), R4		// BootServices
	MOVD	152(R4), R4		// HandleProtocol
	CALL	(R4)

	MOVD	R0, ret+24(FP)
	RET

// uefiBlockIORead calls EFI_BLOCK_IO_PROTOCOL->ReadBlocks
// Go: func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
// UEFI: EFI_STATUS ReadBlocks(EFI_BLOCK_IO_PROTOCOL *This, UINT32 MediaId,
//                              EFI_LBA LBA, UINTN BufferSize, VOID *Buffer)
TEXT ·uefiBlockIORead(SB), NOSPLIT, $0-48
	MOVD	protocol+0(FP), R0	// This
	MOVW	mediaId+8(FP), R1	// MediaId
	MOVD	lba+16(FP), R2		// LBA
	MOVD	bufferSize+24(FP), R3	// BufferSize
	MOVD	buffer+32(FP), R4	// Buffer

	// Get ReadBlocks function pointer (offset 24 in protocol)
	MOVD	24(R0), R5
	CALL	(R5)

	MOVD	R0, ret+40(FP)
	RET

// uefiBlockIOWrite calls EFI_BLOCK_IO_PROTOCOL->WriteBlocks
// Go: func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
// UEFI: EFI_STATUS WriteBlocks(EFI_BLOCK_IO_PROTOCOL *This, UINT32 MediaId,
//                               EFI_LBA LBA, UINTN BufferSize, VOID *Buffer)
TEXT ·uefiBlockIOWrite(SB), NOSPLIT, $0-48
	MOVD	protocol+0(FP), R0	// This
	MOVW	mediaId+8(FP), R1	// MediaId
	MOVD	lba+16(FP), R2		// LBA
	MOVD	bufferSize+24(FP), R3	// BufferSize
	MOVD	buffer+32(FP), R4	// Buffer

	// Get WriteBlocks function pointer (offset 32 in protocol)
	MOVD	32(R0), R5
	CALL	(R5)

	MOVD	R0, ret+40(FP)
	RET

// uefiExitBootServices calls UEFI BootServices->ExitBootServices
// Go: func uefiExitBootServices(imageHandle uintptr, mapKey uintptr) EFI_STATUS
// UEFI: EFI_STATUS ExitBootServices(EFI_HANDLE ImageHandle, UINTN MapKey)
TEXT ·uefiExitBootServices(SB), NOSPLIT, $0-24
	MOVD	imageHandle+0(FP), R0	// ImageHandle
	MOVD	mapKey+8(FP), R1	// MapKey

	// Get BootServices->ExitBootServices (offset 232)
	MOVD	main·systemTable(SB), R4
	MOVD	96(R4), R4		// BootServices
	MOVD	232(R4), R4		// ExitBootServices
	CALL	(R4)

	MOVD	R0, ret+16(FP)
	RET

// uefiGetMemoryMap calls UEFI BootServices->GetMemoryMap
// Go: func uefiGetMemoryMap(memoryMapSize, memoryMap, mapKey, descriptorSize, descriptorVersion *uint64) EFI_STATUS
// UEFI: EFI_STATUS GetMemoryMap(UINTN *MemoryMapSize, EFI_MEMORY_DESCRIPTOR *MemoryMap,
//                                UINTN *MapKey, UINTN *DescriptorSize, UINT32 *DescriptorVersion)
TEXT ·uefiGetMemoryMap(SB), NOSPLIT, $0-48
	MOVD	memoryMapSize+0(FP), R0
	MOVD	memoryMap+8(FP), R1
	MOVD	mapKey+16(FP), R2
	MOVD	descriptorSize+24(FP), R3
	MOVD	descriptorVersion+32(FP), R4

	// Get BootServices->GetMemoryMap (offset 56)
	MOVD	main·systemTable(SB), R5
	MOVD	96(R5), R5		// BootServices
	MOVD	56(R5), R5		// GetMemoryMap
	CALL	(R5)

	MOVD	R0, ret+40(FP)
	RET
