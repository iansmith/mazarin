//go:build !test_stubs

#include "textflag.h"

// RISC-V MMIO primitives for volatile hardware register access.
// Uses regular load/store to MMIO regions. RISC-V requires FENCE
// instructions for ordering guarantees between MMIO and regular memory.

// MmioRead8(addr uintptr) byte
TEXT ·MmioRead8(SB), NOSPLIT, $0-9
	MOV	addr+0(FP), A0
	MOVB	(A0), A0
	MOVB	A0, ret+8(FP)
	RET

// MmioRead16(addr uintptr) uint16
TEXT ·MmioRead16(SB), NOSPLIT, $0-10
	MOV	addr+0(FP), A0
	MOVH	(A0), A0
	MOVH	A0, ret+8(FP)
	RET

// MmioRead32(addr uintptr) uint32
TEXT ·MmioRead32(SB), NOSPLIT, $0-12
	MOV	addr+0(FP), A0
	MOVW	(A0), A0
	MOVW	A0, ret+8(FP)
	RET

// MmioWrite8(addr uintptr, val byte)
TEXT ·MmioWrite8(SB), NOSPLIT, $0-9
	MOV	addr+0(FP), A0
	MOVB	val+8(FP), A1
	MOVB	A1, (A0)
	RET

// MmioWrite32(addr uintptr, val uint32)
TEXT ·MmioWrite32(SB), NOSPLIT, $0-12
	MOV	addr+0(FP), A0
	MOVW	val+8(FP), A1
	MOVW	A1, (A0)
	RET

// MmioWrite16(addr uintptr, val uint16)
TEXT ·MmioWrite16(SB), NOSPLIT, $0-10
	MOV	addr+0(FP), A0
	MOVH	val+8(FP), A1
	MOVH	A1, (A0)
	RET

// Dsb - Full memory fence (FENCE iorw,iorw on RISC-V)
TEXT ·Dsb(SB), NOSPLIT, $0-0
	// FENCE iorw,iorw - orders all I/O and memory operations
	WORD	$0x0FF0000F
	RET

// DmaWmb - Device write barrier (FENCE ow,ow)
// Ensures device stores are ordered with respect to each other.
TEXT ·DmaWmb(SB), NOSPLIT, $0-0
	// FENCE ow,ow (predecessor=ow, successor=ow)
	WORD	$0x0550000F
	RET

// DmaRmb - Device read barrier (FENCE ir,ir)
// Ensures device loads are ordered with respect to each other.
TEXT ·DmaRmb(SB), NOSPLIT, $0-0
	// FENCE ir,ir (predecessor=ir, successor=ir)
	WORD	$0x0AA0000F
	RET

// CleanDCacheRange - Cache flush for DMA coherency
// RISC-V does not have standard cache management instructions in the
// base ISA. QEMU virt machine is cache-coherent, so this is a fence.
TEXT ·CleanDCacheRange(SB), NOSPLIT, $0-16
	// FENCE iorw,iorw
	WORD	$0x0FF0000F
	RET

// InvalidateDCacheRange - Cache invalidate for DMA coherency
// Same as CleanDCacheRange on RISC-V QEMU virt (cache-coherent).
TEXT ·InvalidateDCacheRange(SB), NOSPLIT, $0-16
	// FENCE iorw,iorw
	WORD	$0x0FF0000F
	RET
