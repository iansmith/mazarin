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
	DMB	$2			// DMB OSHST
	RET

// DmaRmb()
// DMA read memory barrier - ensures device writes are visible to CPU.
// DELIBERATELY STRONGER than Linux's dma_rmb(), which is DMB OSHLD (orders
// only earlier loads against later accesses). OSH additionally orders earlier
// stores — margin we keep on purpose (MAZ-177, Ian 2026-08-04): our call
// sites run at hundreds–thousands of ops/sec where the LD variant's saving
// (store buffer keeps draining) is noise, and one shared helper is safer
// full-strength. Revisit per-call-site only if a profile shows a hot DmaRmb
// (e.g. line-rate net RX) — then DMB $1 (OSHLD, 0xD50331BF) with reasoning.
TEXT ·DmaRmb(SB), NOSPLIT, $0-0
	DMB	$3			// DMB OSH
	RET

// CleanDCacheRange(start uintptr, size uintptr)
// Clean data cache for address range (write back to RAM for DMA)
TEXT ·CleanDCacheRange(SB), NOSPLIT, $0-16
	MOVD	start+0(FP), R0		// Start address
	MOVD	size+8(FP), R1		// Size in bytes
	ADD	R0, R1, R1		// R1 = end address
	// [MAZ-179] align start DOWN to the cache line. The old loop began at
	// the unaligned start and strode 64, so an unaligned range SKIPPED its
	// final line(s) — a clean that misses the tail leaves the device
	// reading stale descriptor/data bytes.
	AND	$~63, R0		// R0 = line-aligned start
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

	// [MAZ-179 tier-24 / MAZ-187 — NOT FOR MERGE] neighbor-discard witness.
	// Counts the caller's TRUE request, before the alignment below. A start
	// or end that is not 64-aligned means DC IVAC will discard whatever
	// else shares that head/tail line. R3-R5 are scratch (leaf, NOSPLIT).
	MOVD	·DCacheInvTotal(SB), R3
	ADD	$1, R3
	MOVD	R3, ·DCacheInvTotal(SB)
	AND	$63, R0, R4		// start misalignment
	AND	$63, R1, R5		// end misalignment
	ORR	R5, R4, R4
	CBZ	R4, dcinv_aligned
	MOVD	·DCacheInvUnaligned(SB), R3
	ADD	$1, R3
	MOVD	R3, ·DCacheInvUnaligned(SB)
	MOVD	·DCacheInvFirstVA(SB), R4
	CBNZ	R4, dcinv_aligned	// already recorded the first
	MOVD	R0, ·DCacheInvFirstVA(SB)
	SUB	R0, R1, R4		// length = end - start
	MOVD	R4, ·DCacheInvFirstLen(SB)
dcinv_aligned:

	// [MAZ-179] align start DOWN (same missed-tail bug as CleanDCacheRange).
	// NOTE: DC IVAC on the head/tail lines still discards any neighbor
	// bytes sharing those lines — callers must keep DMA buffers
	// line-isolated; this fix only guarantees the REQUESTED range is
	// actually covered.
	AND	$~63, R0		// R0 = line-aligned start
	MOVD	$64, R2			// Cache line size

loop_inval:
	WORD	$0xD5087620		// DC IVAC, X0 - Invalidate cache line by VA (sys #0, c7, c6, #1, x0)
	ADD	R2, R0			// Move to next cache line
	CMP	R0, R1
	BHI	loop_inval		// Continue while end > current (Plan 9: CMP R0,R1 → ARM CMP X1,X0)

	DSB	$15			// Ensure invalidate completes (DSB SY)
	RET

// Wfe()
// Wait For Event — yields to the hypervisor under HVF.
// Causes a VM exit (trapped via HCR_EL2.TWE) that gives QEMU's event loop
// a scheduling point to process async I/O, then returns immediately.
// Under TCG, WFE is a NOP hint.
TEXT ·Wfe(SB), NOSPLIT, $0-0
	WFE
	RET

// Wfi()
// Wait For Interrupt — halts the vCPU until an interrupt fires.
// Under HVF, this halts the vCPU thread (EXCP_HLT) until QEMU injects an IRQ.
// Under TCG, this stalls emulation until a virtual interrupt fires.
TEXT ·Wfi(SB), NOSPLIT, $0-0
	WFI
	RET

// Hlt()
// No-op on ARM64 (HLT is an x86_64 instruction)
TEXT ·Hlt(SB), NOSPLIT, $0-0
	RET

// StiHlt()
// No-op on ARM64 (STI+HLT is an x86_64 pattern)
TEXT ·StiHlt(SB), NOSPLIT, $0-0
	RET
