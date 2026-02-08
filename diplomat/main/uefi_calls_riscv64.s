// diplomat/main/uefi_calls_riscv64.s
// UEFI function call wrappers for RISC-V 64-bit
//
// RISC-V UEFI uses the standard RISC-V calling convention - the SAME as Go on RISC-V!
// This means wrappers are much simpler than x86_64 (no ABI translation needed).
//
// RISC-V calling convention:
//   - Arguments: A0-A7 (X10-X17, first 8 arguments)
//   - Return: A0-A1 (X10-X11)
//   - Callee-saved: S0-S11 (X8-X9, X18-X27), SP (X2)
//   - Caller-saved: T0-T6 (X5-X7, X28-X31), A0-A7 (X10-X17), RA (X1)

#include "textflag.h"

// uefiAllocatePages calls UEFI BootServices->AllocatePages
// Go: func uefiAllocatePages(allocType, memType uint32, pages uint64, memory *uint64) EFI_STATUS
// UEFI: EFI_STATUS AllocatePages(EFI_ALLOCATE_TYPE Type, EFI_MEMORY_TYPE MemoryType,
//                                 UINTN Pages, EFI_PHYSICAL_ADDRESS *Memory)
TEXT ·uefiAllocatePages(SB), NOSPLIT, $0-40
	MOVW	allocType+0(FP), A0	// Type (32-bit)
	MOVW	memType+4(FP), A1	// MemoryType (32-bit)
	MOV	pages+8(FP), A2		// Pages (64-bit)
	MOV	memory+16(FP), A3	// Memory (out pointer)

	// Get BootServices->AllocatePages
	// systemTable offset 96 = BootServices pointer
	// BootServices offset 40 = AllocatePages
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	40(T0), T0		// AllocatePages
	JALR	RA, (T0)		// Call function

	MOV	A0, ret+24(FP)		// Return EFI_STATUS
	RET

// uefiFreePages calls UEFI BootServices->FreePages
// Go: func uefiFreePages(memory uint64, pages uint64) EFI_STATUS
// UEFI: EFI_STATUS FreePages(EFI_PHYSICAL_ADDRESS Memory, UINTN Pages)
TEXT ·uefiFreePages(SB), NOSPLIT, $0-24
	MOV	memory+0(FP), A0	// Memory
	MOV	pages+8(FP), A1		// Pages

	// Get BootServices->FreePages (offset 48)
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	48(T0), T0		// FreePages
	JALR	RA, (T0)

	MOV	A0, ret+16(FP)
	RET

// uefiHandleProtocol calls UEFI BootServices->HandleProtocol
// Go: func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS
// UEFI: EFI_STATUS HandleProtocol(EFI_HANDLE Handle, EFI_GUID *Protocol, VOID **Interface)
TEXT ·uefiHandleProtocol(SB), NOSPLIT, $0-32
	MOV	handle+0(FP), A0	// Handle
	MOV	protocol+8(FP), A1	// Protocol GUID
	MOV	iface+16(FP), A2	// Interface (out pointer)

	// Get BootServices->HandleProtocol (offset 152)
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	152(T0), T0		// HandleProtocol
	JALR	RA, (T0)

	MOV	A0, ret+24(FP)
	RET

// uefiBlockIORead calls EFI_BLOCK_IO_PROTOCOL->ReadBlocks
// Go: func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
// UEFI: EFI_STATUS ReadBlocks(EFI_BLOCK_IO_PROTOCOL *This, UINT32 MediaId,
//                              EFI_LBA LBA, UINTN BufferSize, VOID *Buffer)
TEXT ·uefiBlockIORead(SB), NOSPLIT, $0-48
	MOV	protocol+0(FP), A0	// This
	MOVW	mediaId+8(FP), A1	// MediaId (32-bit)
	MOV	lba+16(FP), A2		// LBA
	MOV	bufferSize+24(FP), A3	// BufferSize
	MOV	buffer+32(FP), A4	// Buffer

	// Get ReadBlocks function pointer (offset 24 in protocol)
	MOV	24(A0), T0
	JALR	RA, (T0)

	MOV	A0, ret+40(FP)
	RET

// uefiBlockIOWrite calls EFI_BLOCK_IO_PROTOCOL->WriteBlocks
// Go: func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
// UEFI: EFI_STATUS WriteBlocks(EFI_BLOCK_IO_PROTOCOL *This, UINT32 MediaId,
//                               EFI_LBA LBA, UINTN BufferSize, VOID *Buffer)
TEXT ·uefiBlockIOWrite(SB), NOSPLIT, $0-48
	MOV	protocol+0(FP), A0	// This
	MOVW	mediaId+8(FP), A1	// MediaId (32-bit)
	MOV	lba+16(FP), A2		// LBA
	MOV	bufferSize+24(FP), A3	// BufferSize
	MOV	buffer+32(FP), A4	// Buffer

	// Get WriteBlocks function pointer (offset 32 in protocol)
	MOV	32(A0), T0
	JALR	RA, (T0)

	MOV	A0, ret+40(FP)
	RET

// uefiLocateProtocol calls UEFI BootServices->LocateProtocol
// Go: func uefiLocateProtocol(protocol, registration, iface uintptr) EFI_STATUS
// UEFI: EFI_STATUS LocateProtocol(EFI_GUID *Protocol, VOID *Registration, VOID **Interface)
TEXT ·uefiLocateProtocol(SB), NOSPLIT, $0-32
	MOV	protocol+0(FP), A0	// Protocol GUID
	MOV	registration+8(FP), A1	// Registration (NULL)
	MOV	iface+16(FP), A2	// Interface (out pointer)

	// Get BootServices->LocateProtocol (offset 320)
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	320(T0), T0		// LocateProtocol
	JALR	RA, (T0)

	MOV	A0, ret+24(FP)
	RET

// uefiMPGetNumberOfProcessors calls mpProtocol->GetNumberOfProcessors
// Go: func uefiMPGetNumberOfProcessors(protocol, numProcs, numEnabled uintptr) EFI_STATUS
// UEFI: EFI_STATUS GetNumberOfProcessors(THIS, UINTN *NumProcs, UINTN *NumEnabled)
TEXT ·uefiMPGetNumberOfProcessors(SB), NOSPLIT, $0-32
	MOV	protocol+0(FP), A0	// This
	MOV	numProcs+8(FP), A1	// NumProcessors (out)
	MOV	numEnabled+16(FP), A2	// NumEnabledProcessors (out)

	// GetNumberOfProcessors is at offset 8 in the protocol
	MOV	8(A0), T0
	JALR	RA, (T0)

	MOV	A0, ret+24(FP)
	RET

// uefiExitBootServices calls UEFI BootServices->ExitBootServices
// Go: func uefiExitBootServices(imageHandle uintptr, mapKey uintptr) EFI_STATUS
// UEFI: EFI_STATUS ExitBootServices(EFI_HANDLE ImageHandle, UINTN MapKey)
TEXT ·uefiExitBootServices(SB), NOSPLIT, $0-24
	MOV	imageHandle+0(FP), A0	// ImageHandle
	MOV	mapKey+8(FP), A1	// MapKey

	// Get BootServices->ExitBootServices (offset 232)
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	232(T0), T0		// ExitBootServices
	JALR	RA, (T0)

	MOV	A0, ret+16(FP)
	RET

// uefiGetMemoryMap calls UEFI BootServices->GetMemoryMap
// Go: func uefiGetMemoryMap(memoryMapSize, memoryMap, mapKey, descriptorSize, descriptorVersion *uint64) EFI_STATUS
// UEFI: EFI_STATUS GetMemoryMap(UINTN *MemoryMapSize, EFI_MEMORY_DESCRIPTOR *MemoryMap,
//                                UINTN *MapKey, UINTN *DescriptorSize, UINT32 *DescriptorVersion)
TEXT ·uefiGetMemoryMap(SB), NOSPLIT, $0-48
	MOV	memoryMapSize+0(FP), A0
	MOV	memoryMap+8(FP), A1
	MOV	mapKey+16(FP), A2
	MOV	descriptorSize+24(FP), A3
	MOV	descriptorVersion+32(FP), A4

	// Get BootServices->GetMemoryMap (offset 56)
	MOV	main·systemTable(SB), T0
	MOV	96(T0), T0		// BootServices
	MOV	56(T0), T0		// GetMemoryMap
	JALR	RA, (T0)

	MOV	A0, ret+40(FP)
	RET

// ueficall_OutputString calls UEFI ConOut->OutputString
// Go: func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
// UEFI: EFI_STATUS OutputString(EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL *This, CHAR16 *String)
//
// We need to build a null-terminated UCS-2 string on the stack
TEXT ·ueficall_OutputString(SB), NOSPLIT, $32-16
	MOV	conout+0(FP), A0	// This (protocol pointer)

	// Build UCS-2 string on stack: [char, 0x0000]
	MOVHU	char+8(FP), A1		// Load 16-bit char
	ADD	$-16, SP, A2		// A2 = SP-16 (string address)
	MOVH	A1, 0(A2)		// Store char at address
	MOVH	ZERO, 2(A2)		// Store null terminator at +2 bytes

	// String pointer in A1 for call
	MOV	A2, A1

	// Load OutputString function pointer (offset 8 in protocol)
	MOV	8(A0), A2
	JALR	RA, (A2)		// Call OutputString(This, String)

	// Return value in A0, but we don't return it (void function in Go)
	RET
