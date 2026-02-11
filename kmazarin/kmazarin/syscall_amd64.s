//go:build !test_stubs

// syscall_amd64.s - x86_64 SYSCALL MSR helpers and ABI0 stubs

#include "textflag.h"

// readMSR reads a Model-Specific Register.
// func readMSR(msr uint32) uint64
TEXT ·readMSR(SB), NOSPLIT, $0-16
	MOVL	msr+0(FP), CX
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, ret+8(FP)
	RET

// writeMSR writes a Model-Specific Register.
// func writeMSR(msr uint32, val uint64)
TEXT ·writeMSR(SB), NOSPLIT, $0-16
	MOVL	msr+0(FP), CX
	MOVQ	val+8(FP), AX
	MOVQ	AX, DX
	SHRQ	$32, DX			// EDX=high32, EAX=low32
	WRMSR
	RET

// readGDTBase reads the GDT base address via SGDT.
// func readGDTBase() uintptr
TEXT ·readGDTBase(SB), NOSPLIT, $16-8
	// SGDT stores 10 bytes at [RSP]: 2-byte limit + 8-byte base
	BYTE	$0x0F; BYTE $0x01; BYTE $0x04; BYTE $0x24	// SGDT [RSP]
	MOVQ	2(SP), AX		// base at offset 2
	MOVQ	AX, ret+0(FP)
	RET

// readGDTLimit reads the GDT limit via SGDT.
// func readGDTLimit() uint16
TEXT ·readGDTLimit(SB), NOSPLIT, $16-2
	// SGDT stores 10 bytes at [RSP]: 2-byte limit + 8-byte base
	BYTE	$0x0F; BYTE $0x01; BYTE $0x04; BYTE $0x24	// SGDT [RSP]
	MOVW	0(SP), AX		// limit at offset 0
	MOVW	AX, ret+0(FP)
	RET

// writeGDTR loads a new GDT register from the 10-byte descriptor at desc.
// func writeGDTR(desc uintptr)
TEXT ·writeGDTR(SB), NOSPLIT, $0-8
	MOVQ	desc+0(FP), AX
	BYTE	$0x0F; BYTE $0x01; BYTE $0x10		// LGDT [RAX]
	RET

// loadTR loads the Task Register with a TSS selector.
// func loadTR(selector uint16)
TEXT ·loadTR(SB), NOSPLIT, $0-2
	MOVW	selector+0(FP), AX
	BYTE	$0x0F; BYTE $0x00; BYTE $0xD8	// LTR AX
	RET

// getSyscallEntryAddr returns the address of the SYSCALL entry handler
// (defined in exceptions_amd64.s).
// func getSyscallEntryAddr() uintptr
TEXT ·getSyscallEntryAddr(SB), NOSPLIT, $0-8
	LEAQ	·syscallEntry(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

