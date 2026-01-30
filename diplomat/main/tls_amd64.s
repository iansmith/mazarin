// diplomat/main/tls_amd64.s - Thread-Local Storage setup for x86_64 UEFI
//
// In normal programs, the OS sets up TLS (%fs segment) during startup.
// In UEFI, we must do this manually so Go's .abi0 wrappers can load g from %fs:-0x8.

#include "textflag.h"

// SetupTLS initializes the FS segment base to point to the TLS block.
// The TLS block is a 256-byte area with g0 address at offset 0.
//
// On x86_64, there are two ways to set FS base:
//   1. WRFSBASE instruction (requires FSGSBASE CPU feature + CR4.FSGSBASE=1)
//   2. WRMSR to MSR_FS_BASE (0xC0000100) - requires CPL=0
//
// UEFI runs at CPL=0, so we can use WRMSR.
//
// Args: tlsAddr (8 bytes) - address of TLS block
//
//go:nosplit
TEXT ·setupTLS(SB), NOSPLIT, $0-8
	MOVQ tlsAddr+0(FP), AX

	// Use WRMSR to set FS base (MSR 0xC0000100)
	// WRMSR writes EDX:EAX to MSR[ECX]
	MOVQ AX, DX
	SHRQ $32, DX           // DX = upper 32 bits
	// AX already has lower 32 bits
	MOVL $0xC0000100, CX   // MSR_FS_BASE

	// WRMSR instruction (0x0F 0x30)
	BYTE $0x0F
	BYTE $0x30

	RET
