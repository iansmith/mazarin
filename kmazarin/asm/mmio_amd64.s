//go:build !test_stubs

#include "textflag.h"

// x86_64 MMIO primitives for volatile hardware register access.
// On x86_64, MMIO is performed through regular memory operations to
// uncacheable (UC) memory regions. The processor will not reorder or
// cache accesses to UC memory, so no special instructions are needed
// beyond preventing compiler optimization (which assembly achieves).

// MmioRead8(addr uintptr) byte
TEXT ·MmioRead8(SB), NOSPLIT, $0-9
	MOVQ	addr+0(FP), AX
	MOVB	(AX), AX
	MOVB	AX, ret+8(FP)
	RET

// MmioRead16(addr uintptr) uint16
TEXT ·MmioRead16(SB), NOSPLIT, $0-10
	MOVQ	addr+0(FP), AX
	MOVW	(AX), AX
	MOVW	AX, ret+8(FP)
	RET

// MmioRead32(addr uintptr) uint32
TEXT ·MmioRead32(SB), NOSPLIT, $0-12
	MOVQ	addr+0(FP), AX
	MOVL	(AX), AX
	MOVL	AX, ret+8(FP)
	RET

// MmioWrite8(addr uintptr, val byte)
TEXT ·MmioWrite8(SB), NOSPLIT, $0-9
	MOVQ	addr+0(FP), AX
	MOVB	val+8(FP), CX
	MOVB	CX, (AX)
	RET

// MmioWrite32(addr uintptr, val uint32)
TEXT ·MmioWrite32(SB), NOSPLIT, $0-12
	MOVQ	addr+0(FP), AX
	MOVL	val+8(FP), CX
	MOVL	CX, (AX)
	RET

// MmioWrite16(addr uintptr, val uint16)
TEXT ·MmioWrite16(SB), NOSPLIT, $0-10
	MOVQ	addr+0(FP), AX
	MOVW	val+8(FP), CX
	MOVW	CX, (AX)
	RET

// Dsb - Full memory barrier (MFENCE on x86_64)
TEXT ·Dsb(SB), NOSPLIT, $0-0
	MFENCE
	RET

// DmaWmb - Store fence (SFENCE on x86_64)
// Ensures all stores are globally visible before subsequent stores.
TEXT ·DmaWmb(SB), NOSPLIT, $0-0
	SFENCE
	RET

// DmaRmb - Load fence (LFENCE on x86_64)
// Ensures all loads complete before subsequent loads.
TEXT ·DmaRmb(SB), NOSPLIT, $0-0
	LFENCE
	RET

// CleanDCacheRange - No-op on x86_64
// x86_64 has cache-coherent MMIO via UC memory type.
// CLFLUSH/CLFLUSHOPT could be used for non-temporal stores,
// but standard MMIO doesn't require explicit cache management.
TEXT ·CleanDCacheRange(SB), NOSPLIT, $0-16
	MFENCE
	RET

// InvalidateDCacheRange - No-op on x86_64
// x86_64 cache coherency protocol handles this automatically.
TEXT ·InvalidateDCacheRange(SB), NOSPLIT, $0-16
	MFENCE
	RET

// Wfe()
// PAUSE on x86_64 — yields the CPU pipeline, improves spin-wait efficiency.
// Under KVM/HVF, PAUSE can cause a VM exit (pause-loop exiting).
TEXT ·Wfe(SB), NOSPLIT, $0-0
	BYTE $0xF3; BYTE $0x90	// PAUSE instruction
	RET

// Wfi()
// No-op on x86_64 (WFI is an ARM64/RISC-V instruction)
TEXT ·Wfi(SB), NOSPLIT, $0-0
	RET

// Hlt()
// Halt the CPU until the next interrupt fires.
TEXT ·Hlt(SB), NOSPLIT, $0-0
	HLT
	RET
