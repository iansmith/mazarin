//go:build !test_stubs

#include "textflag.h"

// ============================================================================
// OVERVIEW
// ============================================================================
// MMIO (Memory-Mapped I/O) primitives for volatile hardware register access.
// These functions prevent compiler optimization of memory accesses to ensure
// proper hardware interaction.
//
// Functions:
//   - MmioRead8(addr uintptr) byte - Read 8-bit value from MMIO
//   - MmioRead32(addr uintptr) uint32 - Read 32-bit value from MMIO
//   - MmioWrite8(addr uintptr, val byte) - Write 8-bit value to MMIO
//   - MmioWrite32(addr uintptr, val uint32) - Write 32-bit value to MMIO
//
// WHY NOT DECOMPOSE:
// These are already atomic primitives (2-3 instructions each). Each performs
// exactly one volatile memory operation. This is the minimal implementation.
//
// ABI NOTES:
// - ABI0 (stack-based) functions callable from external Go code
// - Arguments read from FP offsets, returns written to ret+N(FP)

// MmioRead8(addr uintptr) byte
// Reads an 8-bit value from a memory-mapped register
TEXT ·MmioRead8(SB), NOSPLIT, $0-9
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVB	(R0), R0		// Read byte from address
	MOVB	R0, ret+8(FP)		// Store return value
	RET

// MmioRead16(addr uintptr) uint16
// Reads a 16-bit value from a memory-mapped register
TEXT ·MmioRead16(SB), NOSPLIT, $0-10
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVH	(R0), R0		// Read 16-bit value from address
	MOVH	R0, ret+8(FP)		// Store return value
	RET

// MmioRead32(addr uintptr) uint32
// Reads a 32-bit value from a memory-mapped register
TEXT ·MmioRead32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	(R0), R0		// Read 32-bit value from address
	MOVW	R0, ret+8(FP)		// Store return value
	RET

// MmioWrite8(addr uintptr, val byte)
// Writes an 8-bit value to a memory-mapped register
TEXT ·MmioWrite8(SB), NOSPLIT, $0-9
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVB	val+8(FP), R1		// Load byte value from stack
	MOVB	R1, (R0)		// Write byte to address
	RET

// MmioWrite32(addr uintptr, val uint32)
// Writes a 32-bit value to a memory-mapped register
TEXT ·MmioWrite32(SB), NOSPLIT, $0-12
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVW	val+8(FP), R1		// Load value from stack
	MOVW	R1, (R0)		// Write 32-bit value to address
	RET

// MmioWrite16(addr uintptr, val uint16)
// Writes a 16-bit value to a memory-mapped register
TEXT ·MmioWrite16(SB), NOSPLIT, $0-10
	MOVD	addr+0(FP), R0		// Load address from stack
	MOVH	val+8(FP), R1		// Load 16-bit value from stack
	MOVH	R1, (R0)		// Write 16-bit value to address
	RET

// Dsb()
// Data Synchronization Barrier - ensures all memory accesses complete
TEXT ·Dsb(SB), NOSPLIT, $0-0
	DSB	$15			// System-wide data synchronization barrier
	RET

// DmaWmb()
// DMA write memory barrier - ensures stores are visible to DMA devices
// Uses DMB OSHST (outer shareable store) like Linux dma_wmb()
TEXT ·DmaWmb(SB), NOSPLIT, $0-0
	WORD	$0xD50332BF		// DMB OSHST (verified with gcc as)
	RET

// DmaRmb()
// DMA read memory barrier - ensures device writes are visible to CPU
// Uses DMB OSH (outer shareable) like Linux dma_rmb()
TEXT ·DmaRmb(SB), NOSPLIT, $0-0
	WORD	$0xD50333BF		// DMB OSH (verified with gcc as)
	RET

// CleanDCacheRange(start uintptr, size uintptr)
// Clean data cache for address range (write back to RAM for DMA)
TEXT ·CleanDCacheRange(SB), NOSPLIT, $0-16
	MOVD	start+0(FP), R0		// Start address
	MOVD	size+8(FP), R1		// Size in bytes
	ADD	R0, R1, R1		// R1 = end address
	MOVD	$64, R2			// Cache line size (64 bytes on most ARM64)

loop_clean:
	WORD	$0xD50B7A20		// DC CVAC, X0 - Clean cache line by VA to PoC (sys #3, c7, c10, #1, x0)
	ADD	R2, R0			// Move to next cache line
	CMP	R0, R1
	BHI	loop_clean		// Continue while end > current (Plan 9: CMP R0,R1 → ARM CMP X1,X0)

	DSB	$15			// Ensure clean completes (DSB SY)
	RET

// InvalidateDCacheRange(start uintptr, size uintptr)
// Invalidate data cache for address range (discard cache, read from RAM)
TEXT ·InvalidateDCacheRange(SB), NOSPLIT, $0-16
	MOVD	start+0(FP), R0		// Start address
	MOVD	size+8(FP), R1		// Size in bytes
	ADD	R0, R1, R1		// R1 = end address
	MOVD	$64, R2			// Cache line size

loop_inval:
	WORD	$0xD5087620		// DC IVAC, X0 - Invalidate cache line by VA (sys #0, c7, c6, #1, x0)
	ADD	R2, R0			// Move to next cache line
	CMP	R0, R1
	BHI	loop_inval		// Continue while end > current (Plan 9: CMP R0,R1 → ARM CMP X1,X0)

	DSB	$15			// Ensure invalidate completes (DSB SY)
	RET
