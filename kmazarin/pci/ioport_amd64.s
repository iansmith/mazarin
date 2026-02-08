#include "textflag.h"

// pciIOConfigRead32 reads a 32-bit value from PCI config space via I/O ports.
// Uses the traditional CF8/CFC mechanism (works without ECAM mapping).
//
// func pciIOConfigRead32(addr uint32) uint32
TEXT ·pciIOConfigRead32(SB), NOSPLIT, $0-12
	// Write config address to 0xCF8
	MOVL	addr+0(FP), AX
	MOVW	$0x0CF8, DX
	OUTL
	// Read config data from 0xCFC
	MOVW	$0x0CFC, DX
	INL
	MOVL	AX, ret+8(FP)
	RET

// pciIOConfigWrite32 writes a 32-bit value to PCI config space via I/O ports.
//
// func pciIOConfigWrite32(addr uint32, value uint32)
TEXT ·pciIOConfigWrite32(SB), NOSPLIT, $0-8
	// Write config address to 0xCF8
	MOVL	addr+0(FP), AX
	MOVW	$0x0CF8, DX
	OUTL
	// Write config data to 0xCFC
	MOVL	value+4(FP), AX
	MOVW	$0x0CFC, DX
	OUTL
	RET
