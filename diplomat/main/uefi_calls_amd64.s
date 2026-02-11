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
TEXT ·ueficall_OutputString(SB), NOSPLIT, $56-16
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
TEXT ·uefiCallAllocatePages(SB), NOSPLIT, $40-40
	// Stack frame: 40 bytes (32 shadow + 8 BP save by Go)
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
TEXT ·uefiCallFreePages(SB), NOSPLIT, $40-32
	// Stack frame: 40 bytes (32 shadow + 8 BP save by Go)
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
TEXT ·uefiCallBlockIORead(SB), NOSPLIT, $56-56
	// Stack frame: 56 bytes (32 shadow + 8 for 5th arg + 8 BP save + 8 alignment)

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
TEXT ·uefiCallBlockIOWrite(SB), NOSPLIT, $56-56
	// Stack frame: 56 bytes (32 shadow + 8 for 5th arg + 8 BP save + 8 alignment)

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

// func uefiHandleProtocol(handle, protocol, iface, funcPtr uintptr) EFI_STATUS
//
// Calls EFI_BOOT_SERVICES.HandleProtocol with Microsoft x64 calling convention:
//   EFI_STATUS HandleProtocol(
//       IN EFI_HANDLE Handle,           // RCX
//       IN EFI_GUID *Protocol,          // RDX
//       OUT VOID **Interface            // R8
//   );
//
// Go arguments on stack (ABI0):
//   handle+0(FP)   - 8 bytes (uintptr)
//   protocol+8(FP) - 8 bytes (uintptr)
//   iface+16(FP)   - 8 bytes (uintptr)
//   funcPtr+24(FP) - 8 bytes (uintptr)
//   ret+32(FP)     - 8 bytes (EFI_STATUS)
TEXT ·uefiHandleProtocol(SB), NOSPLIT, $40-40
	// Stack frame: 40 bytes
	// Go inserts PUSHQ BP (8 bytes), leaving 32 bytes for shadow space
	// Frame size 40 mod 16 == 8, so RSP is 16-aligned at CALL point:
	//   SP = original - 8(ret) - 8(BP) - 32(locals) = original - 48 = 0 mod 16

	// DEBUG: 'H' = entering HandleProtocol wrapper (AX/DX free before arg load)
	MOVB $'H', AL
	MOVW $0xE9, DX
	OUTB

	// Load arguments into MS x64 ABI registers
	MOVQ handle+0(FP), CX       // RCX = Handle
	MOVQ protocol+8(FP), DX     // RDX = Protocol GUID pointer
	MOVQ iface+16(FP), R8       // R8 = Interface output pointer
	MOVQ funcPtr+24(FP), AX     // AX = function pointer

	// DEBUG: 'I' = about to CALL (use R9 as scratch, not needed by HandleProtocol)
	MOVQ AX, R9                 // Save func ptr
	MOVQ DX, R10                // Save protocol ptr
	MOVB $'I', AL
	MOVW $0xE9, DX
	OUTB
	MOVQ R9, AX                 // Restore func ptr
	MOVQ R10, DX                // Restore protocol ptr

	CALL AX

	// DEBUG: 'J' = returned from CALL (save return value)
	MOVQ AX, R9                 // Save EFI_STATUS
	MOVB $'J', AL
	MOVW $0xE9, DX
	OUTB
	MOVQ R9, AX                 // Restore EFI_STATUS

	// Return EFI_STATUS
	MOVQ AX, ret+32(FP)
	RET

// func debugPortOut(c byte)
//
// Writes a byte to QEMU debug port 0xE9
TEXT ·debugPortOut(SB), NOSPLIT, $0-1
	MOVB c+0(FP), AL
	MOVW $0xE9, DX
	OUTB
	RET

// func uefiCallGetMemoryMap(funcPtr uintptr, mapSize, mapBuf, mapKey, descSize, descVer *uint64) EFI_STATUS
//
// Calls UEFI GetMemoryMap with Microsoft x64 calling convention:
//   EFI_STATUS GetMemoryMap(
//       IN OUT UINTN *MemoryMapSize,             // RCX
//       OUT EFI_MEMORY_DESCRIPTOR *MemoryMap,     // RDX
//       OUT UINTN *MapKey,                        // R8
//       OUT UINTN *DescriptorSize,                // R9
//       OUT UINT32 *DescriptorVersion             // Stack at 32(RSP)
//   );
TEXT ·uefiCallGetMemoryMap(SB), NOSPLIT, $56-56
	// Load arguments into MS x64 ABI registers
	MOVQ mapSize+8(FP), CX      // RCX = MemoryMapSize pointer
	MOVQ mapBuf+16(FP), DX      // RDX = MemoryMap buffer
	MOVQ mapKey+24(FP), R8      // R8 = MapKey pointer
	MOVQ descSize+32(FP), R9    // R9 = DescriptorSize pointer

	// 5th argument goes on stack after 32-byte shadow space
	MOVQ descVer+40(FP), AX
	MOVQ AX, 32(SP)

	// Load function pointer and call
	MOVQ funcPtr+0(FP), AX
	CALL AX

	// Return EFI_STATUS
	MOVQ AX, ret+48(FP)
	RET

// func uefiCallExitBootServices(funcPtr uintptr, imageHandle uintptr, mapKey uint64) EFI_STATUS
//
// Calls UEFI ExitBootServices with Microsoft x64 calling convention:
//   EFI_STATUS ExitBootServices(
//       IN EFI_HANDLE ImageHandle,   // RCX
//       IN UINTN MapKey              // RDX
//   );
TEXT ·uefiCallExitBootServices(SB), NOSPLIT, $40-32
	// Load arguments into MS x64 ABI registers
	MOVQ imageHandle+8(FP), CX  // RCX = ImageHandle
	MOVQ mapKey+16(FP), DX      // RDX = MapKey

	// Load function pointer and call
	MOVQ funcPtr+0(FP), AX
	CALL AX

	// Return EFI_STATUS
	MOVQ AX, ret+24(FP)
	RET

// func uefiCallLocateProtocol(funcPtr, protocol, registration, iface uintptr) EFI_STATUS
//
// Calls UEFI LocateProtocol with Microsoft x64 calling convention:
//   EFI_STATUS LocateProtocol(
//       IN EFI_GUID *Protocol,      // RCX
//       IN VOID *Registration,      // RDX
//       OUT VOID **Interface        // R8
//   );
TEXT ·uefiCallLocateProtocol(SB), NOSPLIT, $40-40
	MOVQ protocol+8(FP), CX      // RCX = Protocol GUID
	MOVQ registration+16(FP), DX  // RDX = Registration (NULL)
	MOVQ iface+24(FP), R8        // R8 = Interface (out pointer)

	MOVQ funcPtr+0(FP), AX
	CALL AX

	MOVQ AX, ret+32(FP)
	RET

// func uefiCallMPGetNumberOfProcessors(funcPtr, protocol, numProcs, numEnabled uintptr) EFI_STATUS
//
// Calls MP Services GetNumberOfProcessors with Microsoft x64 calling convention:
//   EFI_STATUS GetNumberOfProcessors(
//       IN EFI_MP_SERVICES_PROTOCOL *This,  // RCX
//       OUT UINTN *NumberOfProcessors,      // RDX
//       OUT UINTN *NumberOfEnabledProcessors // R8
//   );
TEXT ·uefiCallMPGetNumberOfProcessors(SB), NOSPLIT, $40-40
	MOVQ protocol+8(FP), CX     // RCX = This
	MOVQ numProcs+16(FP), DX    // RDX = NumberOfProcessors (out)
	MOVQ numEnabled+24(FP), R8  // R8 = NumberOfEnabledProcessors (out)

	MOVQ funcPtr+0(FP), AX
	CALL AX

	MOVQ AX, ret+32(FP)
	RET

// func readTimerCounter() uint64
// Reads TSC (Time Stamp Counter) via RDTSC.
// Returns EDX:EAX as a 64-bit value.
TEXT ·readTimerCounter(SB), NOSPLIT, $0-8
	RDTSC
	SHLQ $32, DX
	ORQ DX, AX
	MOVQ AX, ret+0(FP)
	RET

// func readCR3() uint64
TEXT ·readCR3(SB), NOSPLIT, $0-8
	MOVQ CR3, AX
	MOVQ AX, ret+0(FP)
	RET

// func writeCR3(val uint64)
TEXT ·writeCR3(SB), NOSPLIT, $0-8
	MOVQ val+0(FP), AX
	MOVQ AX, CR3
	RET

// func disableWriteProtect()
// Clears CR0.WP (bit 16) to allow supervisor writes to read-only pages
TEXT ·disableWriteProtect(SB), NOSPLIT, $0-0
	MOVQ CR0, AX
	ANDQ $~(1<<16), AX
	MOVQ AX, CR0
	RET

// func enableWriteProtect()
// Sets CR0.WP (bit 16) to enforce write protection
TEXT ·enableWriteProtect(SB), NOSPLIT, $0-0
	MOVQ CR0, AX
	ORQ $(1<<16), AX
	MOVQ AX, CR0
	RET

// func jumpToEntry(entry uint64)
//
// Jumps to a kernel entry point. Does not return.
// The entry point is called with no arguments in a clean state.
TEXT ·jumpToEntry(SB), NOSPLIT, $0-8
	MOVQ entry+0(FP), AX

	// Clear registers for clean state
	XORQ BX, BX
	XORQ CX, CX
	XORQ DX, DX
	XORQ SI, SI
	XORQ DI, DI
	XORQ BP, BP
	XORQ R8, R8
	XORQ R9, R9
	XORQ R10, R10
	XORQ R11, R11
	XORQ R12, R12
	XORQ R13, R13
	XORQ R14, R14
	XORQ R15, R15

	// Jump to entry point (no return)
	JMP AX

// func jumpToKernelWithCR3(pml4Phys, virtEntry uint64)
//
// Switches CR3 to new page tables and jumps to kernel at virtual entry point.
// Identity mapping for low memory must be present in the new page tables
// so this code can continue executing after the CR3 switch.
// Does not return.
TEXT ·jumpToKernelWithCR3(SB), NOSPLIT, $0-16
	MOVQ pml4Phys+0(FP), AX
	MOVQ virtEntry+8(FP), BX

	// Load new page tables into CR3
	// This immediately switches the virtual-to-physical mapping.
	// Because we include identity mapping for low memory in the new tables,
	// this code (running at low physical addresses) continues to work.
	MOVQ AX, CR3

	// Clear registers for clean state (except BX which has entry)
	XORQ AX, AX
	XORQ CX, CX
	XORQ DX, DX
	XORQ SI, SI
	XORQ DI, DI
	XORQ BP, BP
	XORQ R8, R8
	XORQ R9, R9
	XORQ R10, R10
	XORQ R11, R11
	XORQ R12, R12
	XORQ R13, R13
	XORQ R14, R14
	XORQ R15, R15

	// Jump to kernel virtual entry point (no return)
	JMP BX

// func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, idtBase uint64)
//
// Sets up extended GDT (Ring 0 + Ring 3 + TSS), configures SYSCALL MSRs,
// installs diplomat's demand-paging IDT, switches RSP, and jumps to kernel.
// Does not return.
//
// entry:       kernel entry point (virtual address)
// g0StackPtr:  RSP value pointing to argc on g0 stack (VA)
// excStackTop: exception stack top (VA) — used for TSS RSP0
// idtBase:     kmazarin's ExceptionVectorTable address (unused on x86_64)
TEXT ·jumpToKmazarinWithStack(SB), NOSPLIT, $0-32
	MOVQ	entry+0(FP), AX
	MOVQ	g0StackPtr+8(FP), BX	// new RSP (saved for later)
	MOVQ	excStackTop+16(FP), CX	// exception stack top for TSS RSP0

	// Save entry, g0StackPtr, excStackTop in callee-saved registers
	MOVQ	AX, R12			// entry
	MOVQ	BX, R13			// g0StackPtr
	MOVQ	CX, R14			// excStackTop

	// Disable interrupts — UEFI timer interrupts could fire during
	// kmazarin init and hit our minimal IDT.
	CLI

	// ================================================================
	// Step 1: Create fresh GDT with standard layout
	// ================================================================
	// Standard x86_64 GDT layout:
	//   0x00: Null descriptor
	//   0x08: Ring 0 code (64-bit, present, DPL=0, executable, readable)
	//   0x10: Ring 0 data (64-bit, present, DPL=0, writable)
	//   0x18: Ring 3 code (64-bit, present, DPL=3, executable, readable)
	//   0x20: Ring 3 data (64-bit, present, DPL=3, writable)
	//   0x28: TSS (will be filled in Step 2)

	// DEBUG: Entering jumpToKmazarinWithStack
	MOVB	$'1', AL
	MOVW	$0xE9, DX
	OUTB

	LEAQ	·newGDTBuffer(SB), R9	// R9 = GDT base
	LEAQ	·gdtrDesc(SB), DI	// DI = GDTR descriptor address

	// Zero the entire GDT buffer first
	MOVQ	$0, R10
zero_gdt_loop:
	CMPQ	R10, $512
	JGE	zero_gdt_done
	MOVB	$0, 0(R9)(R10*1)
	INCQ	R10
	JMP	zero_gdt_loop
zero_gdt_done:

	// 0x00: Null descriptor (already zeroed)

	// 0x08: Ring 0 code descriptor
	// Limit=0xFFFFF, base=0, access=0x9A (P=1,DPL=0,S=1,Type=0xA=ExecRead),
	// flags=0xA (G=1,L=1,limit[19:16]=0xF)
	// As uint64: 0x00AF9A000000FFFF
	MOVQ	$0x00AF9A000000FFFF, R10
	MOVQ	R10, 0x08(R9)

	// 0x10: Ring 0 data descriptor
	// Limit=0xFFFFF, base=0, access=0x92 (P=1,DPL=0,S=1,Type=0x2=RW),
	// flags=0xC (G=1,DB=1,limit[19:16]=0xF)
	// As uint64: 0x00CF92000000FFFF
	MOVQ	$0x00CF92000000FFFF, R10
	MOVQ	R10, 0x10(R9)

	// 0x18: Ring 3 code descriptor
	// Limit=0xFFFFF, base=0, access=0xFA (P=1,DPL=3,S=1,Type=0xA=ExecRead),
	// flags=0xA (G=1,L=1,limit[19:16]=0xF)
	// As uint64: 0x00AFFA000000FFFF
	MOVQ	$0x00AFFA000000FFFF, R10
	MOVQ	R10, 0x18(R9)

	// 0x20: Ring 3 data descriptor
	// Limit=0xFFFFF, base=0, access=0xF2 (P=1,DPL=3,S=1,Type=0x2=RW),
	// flags=0xC (G=1,DB=1,limit[19:16]=0xF)
	// As uint64: 0x00CFF2000000FFFF
	MOVQ	$0x00CFF2000000FFFF, R10
	MOVQ	R10, 0x20(R9)

	// ================================================================
	// Step 2: Build TSS at 0x28 (16-byte system descriptor in 64-bit mode)
	// ================================================================

	// Zero the tssBuffer (128 bytes)
	LEAQ	·tssBuffer(SB), R15
	MOVQ	$0, R10
zero_tss_loop:
	CMPQ	R10, $128
	JGE	zero_tss_done
	MOVB	$0, 0(R15)(R10*1)
	INCQ	R10
	JMP	zero_tss_loop
zero_tss_done:

	// Write RSP0 = excStackTop (R14) at TSS offset 4-11
	MOVQ	R14, 4(R15)

	// Write IOPB offset = 104 at TSS offset 102-103
	MOVW	$104, 102(R15)

	// Build TSS descriptor at GDT offset 0x28
	// TSS base = address of tssBuffer (R15)
	// TSS limit = 103 (104 bytes - 1)
	MOVQ	R15, R10		// R10 = TSS base address

	// Bytes 0-1: limit[15:0] = 103 = 0x0067
	MOVW	$0x0067, 0x28(R9)

	// Bytes 2-3: base[15:0]
	MOVW	R10, 0x2A(R9)

	// Byte 4: base[23:16]
	MOVQ	R10, R11
	SHRQ	$16, R11
	MOVB	R11, 0x2C(R9)

	// Byte 5: access = 0x89 (Present, DPL=0, type=9 = available 64-bit TSS)
	MOVB	$0x89, 0x2D(R9)

	// Byte 6: flags[3:0] | limit[19:16] = 0x00 (limit fits in 16 bits)
	MOVB	$0x00, 0x2E(R9)

	// Byte 7: base[31:24]
	MOVQ	R10, R11
	SHRQ	$24, R11
	MOVB	R11, 0x2F(R9)

	// Bytes 8-11: base[63:32]
	MOVQ	R10, R11
	SHRQ	$32, R11
	MOVL	R11, 0x30(R9)

	// Bytes 12-15: reserved = 0
	MOVL	$0, 0x34(R9)

	// ================================================================
	// Step 3: Load new GDT (limit=0x37 covers through TSS at 0x28-0x37)
	// ================================================================
	// DEBUG: Before LGDT
	MOVB	$'2', AL
	MOVW	$0xE9, DX
	OUTB

	MOVW	$0x0037, 0(DI)		// limit = 0x37 (gdtrDesc[0:1])
	MOVQ	R9, 2(DI)		// base = newGDTBuffer (gdtrDesc[2:9])
	BYTE	$0x0F; BYTE $0x01; BYTE $0x17	// LGDT [RDI]

	// DEBUG: After LGDT, before LTR
	MOVB	$'3', AL
	MOVW	$0xE9, DX
	OUTB

	// Load Task Register with TSS selector 0x28
	MOVW	$0x0028, AX
	BYTE	$0x0F; BYTE $0x00; BYTE $0xD8	// LTR AX

	// DEBUG: After LTR
	MOVB	$'L', AL
	MOVW	$0xE9, DX
	OUTB

	// ================================================================
	// Step 4: Reload CS from UEFI's 0x38 to our new 0x08
	// ================================================================
	// DEBUG: Before far return
	MOVB	$'F', AL
	MOVW	$0xE9, DX
	OUTB
	// Use far return to atomically reload CS and jump to next instruction
	// Calculate RIP of next instruction using CALL trick
	BYTE	$0xE8; BYTE $0x00; BYTE $0x00; BYTE $0x00; BYTE $0x00  // CALL $+5 (push RIP+5)
	POPQ	AX			// AX = RIP of instruction after CALL
	ADDQ	$10, AX			// Skip 10 bytes: POPQ(1) + ADDQ(4) + PUSHQ(2) + PUSHQ(1) + RETFQ(2) = 10
	PUSHQ	$0x08			// New CS selector (2 bytes) - goes to [RSP], will be at [RSP+8] after next push
	PUSHQ	AX			// Return address (cs_reloaded) (1 byte) - goes to [RSP]
	BYTE	$0x48; BYTE $0xCB	// RETFQ (far return) (2 bytes) - pops RIP from [RSP], CS from [RSP+8]

cs_reloaded:
	// DEBUG: After far return, CS reloaded
	MOVB	$'4', AL
	MOVW	$0xE9, DX
	OUTB

	// Now running with CS=0x08. Reload data segment registers.
	MOVW	$0x10, AX		// Ring 0 data selector
	MOVW	AX, DS
	MOVW	AX, ES
	MOVW	AX, SS
	XORW	AX, AX
	MOVW	AX, FS			// Zero out FS (kmazarin sets FS_BASE via MSR)
	MOVW	AX, GS			// Zero out GS

	// ================================================================
	// Step 5: Configure SYSCALL MSRs
	// ================================================================
	// DEBUG: Before SYSCALL setup
	MOVB	$'5', AL
	MOVW	$0xE9, DX
	OUTB

	// 5a. Enable SYSCALL in EFER (set SCE bit 0)
	MOVL	$0xC0000080, CX		// MSR_EFER
	RDMSR
	ORL	$1, AX			// Set SCE bit
	WRMSR

	// 5b. Set STAR: SYSCALL CS = 0x08 in bits [47:32]
	//     SYSRET base = 0x18 in bits [63:48] (not used, we use IRETQ)
	MOVL	$0xC0000081, CX		// MSR_STAR
	MOVL	$0, AX			// Low 32 bits = 0 (32-bit SYSCALL EIP, unused)
	MOVL	$0x00180008, DX		// High 32 bits: [31:16]=0x18 (SYSRET base), [15:0]=0x08 (SYSCALL CS)
	WRMSR

	// 5c. Set LSTAR = kmazarin's syscallEntry address
	MOVL	$0xC0000082, CX		// MSR_LSTAR
	MOVQ	·kmazarinSyscallEntry(SB), R10
	MOVQ	R10, AX
	MOVQ	R10, DX
	SHRQ	$32, DX			// EDX=high32, EAX=low32
	WRMSR

	// 5d. Set FMASK = 0x200 (clear IF on SYSCALL entry)
	MOVL	$0xC0000084, CX		// MSR_FMASK
	MOVL	$0x200, AX
	XORL	DX, DX
	WRMSR

	// 5e. Initialize FS_BASE for Go TLS (kmazarin expects g at FS_BASE-8)
	// Allocate initial TLS buffer (16 bytes: 8 for alignment + 8 for g pointer)
	LEAQ	·initialTLSBuf(SB), R10
	ADDQ	$16, R10		// Point to end of buffer (FS_BASE)
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	MOVQ	R10, AX
	MOVQ	R10, DX
	SHRQ	$32, DX			// EDX=high32, EAX=low32
	WRMSR

	// ================================================================
	// Step 6: Install diplomat's demand-paging IDT and jump to kmazarin
	// ================================================================
	// DEBUG: Before LIDT
	MOVB	$'6', AL
	MOVW	$0xE9, DX
	OUTB

	LEAQ	·idtrDescriptor(SB), SI
	BYTE	$0x0F; BYTE $0x01; BYTE $0x1E	// LIDT [RSI]

	// DEBUG: After LIDT
	MOVB	$'7', AL
	MOVW	$0xE9, DX
	OUTB

	// Switch RSP to g0 stack (pointing to argc/argv/auxv)
	MOVQ	R13, SP

	// DEBUG: Output 'J' before jump
	MOVQ	$0xE9, DX
	MOVQ	$'J', AX
	OUTB

	// Clear registers for clean state
	XORQ	BX, BX
	XORQ	CX, CX
	XORQ	DX, DX
	XORQ	SI, SI
	XORQ	DI, DI
	XORQ	BP, BP
	XORQ	R8, R8
	XORQ	R9, R9
	XORQ	R10, R10
	XORQ	R11, R11
	// DEBUG: Output 'K' right before jump (use R10, not AL which holds entry point!)
	MOVQ	$'K', R10
	MOVW	$0xE9, DX
	MOVB	R10, AL
	OUTB

	MOVQ	R12, AX			// entry point (load AFTER debug output)
	XORQ	R12, R12
	XORQ	R13, R13
	XORQ	R14, R14
	XORQ	R15, R15

	// Jump to kmazarin entry point (_rt0_amd64_linux)
	JMP	AX

// diplomat data sections for GDT/TSS setup
GLOBL	·newGDTBuffer(SB), NOPTR, $512
GLOBL	·gdtrDesc(SB), NOPTR, $10
GLOBL	·initialTLSBuf(SB), NOPTR, $16	// Initial TLS buffer for FS_BASE
GLOBL	·tssBuffer(SB), NOPTR, $128
