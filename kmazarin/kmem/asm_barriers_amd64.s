
// asm_barriers_amd64.s - x86_64 memory barriers and TLB operations
//
// x86_64 equivalents of ARM64 barriers, TLB invalidation, and cache ops.
// x86_64 has strong memory ordering, so many barriers are lighter than ARM64.

#include "textflag.h"

// dsbSYAsm - Data Synchronization Barrier (full system)
// x86_64: MFENCE provides full memory ordering
TEXT ·dsbSYAsm(SB), NOSPLIT, $0
	MFENCE
	RET

// tlbiVAE1ISAsm - Invalidate TLB entry by VA
// x86_64: INVLPG invalidates a single TLB entry
// va parameter is the shifted VA (va >> 12), so we shift it back
TEXT ·tlbiVAE1ISAsm(SB), NOSPLIT, $0-8
	MOVQ	va+0(FP), AX
	SHLQ	$12, AX		// Restore full VA from shifted value
	INVLPG	(AX)
	RET

// tlbiVMALLE1 - Invalidate ALL TLB entries
// x86_64: Reload CR3 to flush entire TLB
TEXT ·tlbiVMALLE1(SB), NOSPLIT, $0-0
	MOVQ	CR3, AX
	MOVQ	AX, CR3
	RET

// tlbiVMALLE1IS - Invalidate ALL TLB entries Inner Shareable
// x86_64: Same as tlbiVMALLE1 (no inner/outer distinction on x86)
TEXT ·tlbiVMALLE1IS(SB), NOSPLIT, $0-0
	MOVQ	CR3, AX
	MOVQ	AX, CR3
	RET

// tlbiASIDE1ISAsm - Invalidate TLB entries by ASID
// x86_64: No ASID concept — flush entire TLB via CR3 reload
TEXT ·tlbiASIDE1ISAsm(SB), NOSPLIT, $0-2
	MOVQ	CR3, AX
	MOVQ	AX, CR3
	RET

// dsbISH - Data Synchronization Barrier Inner Shareable
// x86_64: MFENCE
TEXT ·dsbISH(SB), NOSPLIT, $0-0
	MFENCE
	RET

// isbSYAsm - Instruction Synchronization Barrier
// x86_64: LFENCE for serialization (x86 is mostly self-synchronizing)
TEXT ·isbSYAsm(SB), NOSPLIT, $0
	LFENCE
	RET

// readCR3 - Read CR3 register (page table base)
// Returns the raw CR3 value: [63:12]=PML4 PA, [11:0]=PCID/flags
// Use readCurrentL0PA() in Go code to get the L0 page table PA.
TEXT ·readCR3(SB), NOSPLIT, $0-8
	MOVQ	CR3, AX
	MOVQ	AX, ret+0(FP)
	RET

// dcCIVACAsm - Clean and Invalidate Data Cache by VA to PoC
// x86_64: CLFLUSH flushes a cache line
TEXT ·dcCIVACAsm(SB), NOSPLIT, $0-8
	MOVQ	va+0(FP), AX
	BYTE	$0x0F; BYTE	$0xAE; BYTE	$0x38	// CLFLUSH (AX)
	RET

// dcCVAUAsm - Clean Data Cache by VA to Point of Unification
// x86_64: CLFLUSH (same as CIVAC on x86)
TEXT ·dcCVAUAsm(SB), NOSPLIT, $0-8
	MOVQ	va+0(FP), AX
	BYTE	$0x0F; BYTE	$0xAE; BYTE	$0x38	// CLFLUSH (AX)
	RET

// icIVAUAsm - Invalidate Instruction Cache by VA
// x86_64: No-op (x86 I-cache is coherent with D-cache)
TEXT ·icIVAUAsm(SB), NOSPLIT, $0-8
	RET

// icIALLUAsm - Invalidate ALL Instruction Cache
// x86_64: No-op (x86 I-cache is coherent with D-cache)
TEXT ·icIALLUAsm(SB), NOSPLIT, $0-0
	RET

// dcZVAAsm - Zero a single cache line
// x86_64: Zero 64 bytes at the given address
TEXT ·dcZVAAsm(SB), NOSPLIT, $0-8
	MOVQ	addr+0(FP), DI
	XORQ	AX, AX
	MOVQ	AX, 0(DI)
	MOVQ	AX, 8(DI)
	MOVQ	AX, 16(DI)
	MOVQ	AX, 24(DI)
	MOVQ	AX, 32(DI)
	MOVQ	AX, 40(DI)
	MOVQ	AX, 48(DI)
	MOVQ	AX, 56(DI)
	RET

// bzero4KAsm - Zero a 4KB page
// x86_64: Zero 4096 bytes using REP STOSQ
TEXT ·bzero4KAsm(SB), NOSPLIT, $0-8
	MOVQ	ptr+0(FP), DI
	XORQ	AX, AX
	MOVQ	$512, CX	// 4096/8 = 512 quadwords
	REP
	STOSQ
	RET

// writeTTBR0Asm - Write page table base register
// x86_64: Write CR3. Mask off ASID bits [63:48] which are used by
// ARM64 TTBR0 but must be zero in x86_64 CR3 (no PCID).
TEXT ·writeTTBR0Asm(SB), NOSPLIT, $0-8
	MOVQ	val+0(FP), AX
	MOVQ	$0x0000FFFFFFFFFFFF, CX
	ANDQ	CX, AX			// Strip ASID from high bits
	MOVQ	AX, CR3
	RET

// atS1E0R - Address Translation Stage 1 EL0 Read
// x86_64: No direct equivalent. Return 1 (bit 0 set = fault) to indicate failure.
// Page table walking on x86_64 must be done in software.
TEXT ·atS1E0R(SB), NOSPLIT, $0-16
	MOVQ	$1, ret+8(FP)	// Return fault indicator
	RET

// dcCleanInvalidateAll - Clean and Invalidate entire D-cache
// x86_64: WBINVD flushes all caches
TEXT ·dcCleanInvalidateAll(SB), NOSPLIT, $0-0
	WBINVD
	RET
