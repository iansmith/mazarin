
// asm_barriers_riscv64.s - RISC-V memory barriers and TLB operations
//
// RISC-V equivalents of ARM64 barriers, TLB invalidation, and cache ops.
// RISC-V uses FENCE for memory ordering and SFENCE.VMA for TLB management.

#include "textflag.h"

// dsbSYAsm - Data Synchronization Barrier (full system)
// RISC-V: FENCE rw,rw provides full memory ordering
TEXT ·dsbSYAsm(SB), NOSPLIT, $0
	// FENCE rw,rw = 0x0330000F
	WORD	$0x0330000F
	RET

// tlbiVAE1ISAsm - Invalidate TLB entry by VA
// RISC-V: SFENCE.VMA invalidates TLB entries
// va parameter is the shifted VA (va >> 12), so we shift it back
TEXT ·tlbiVAE1ISAsm(SB), NOSPLIT, $0-8
	MOV	va+0(FP), A0
	SLL	$12, A0		// Restore full VA from shifted value
	// SFENCE.VMA A0, zero (invalidate specific VA)
	WORD	$0x12050073
	RET

// tlbiVMALLE1 - Invalidate ALL TLB entries
// RISC-V: SFENCE.VMA zero,zero flushes entire TLB
TEXT ·tlbiVMALLE1(SB), NOSPLIT, $0-0
	// SFENCE.VMA zero,zero = 0x12000073
	WORD	$0x12000073
	RET

// tlbiVMALLE1IS - Invalidate ALL TLB entries Inner Shareable
// RISC-V: Same as tlbiVMALLE1 (SFENCE.VMA broadcasts on multi-core)
TEXT ·tlbiVMALLE1IS(SB), NOSPLIT, $0-0
	// SFENCE.VMA zero,zero = 0x12000073
	WORD	$0x12000073
	RET

// tlbiASIDE1ISAsm - Invalidate TLB entries by ASID
// RISC-V: SFENCE.VMA zero,asid (invalidate all VAs for given ASID)
// asid parameter goes in rs2 field
TEXT ·tlbiASIDE1ISAsm(SB), NOSPLIT, $0-8
	MOV	asid+0(FP), A0
	// SFENCE.VMA zero, A0 (flush all TLB entries for ASID in A0)
	// Encoding: 0x12050073 with rs2=A0
	WORD	$0x12050073
	RET

// dsbISH - Data Synchronization Barrier Inner Shareable
// RISC-V: FENCE rw,rw
TEXT ·dsbISH(SB), NOSPLIT, $0-0
	// FENCE rw,rw = 0x0330000F
	WORD	$0x0330000F
	RET

// isbSYAsm - Instruction Synchronization Barrier
// RISC-V: FENCE.I ensures instruction fetch sees memory updates
TEXT ·isbSYAsm(SB), NOSPLIT, $0
	// FENCE.I = 0x0000100F
	WORD	$0x0000100F
	RET

// readSATP - Read SATP register (Supervisor Address Translation and Protection)
// Returns the raw SATP value: [63:60]=MODE, [59:44]=ASID, [43:0]=PPN
// Use readCurrentL0PA() in Go code to get the L0 page table PA.
TEXT ·readSATP(SB), NOSPLIT, $0-8
	// CSRR A0, satp (satp = 0x180)
	WORD	$0x18002573
	MOV	A0, ret+0(FP)
	RET

// dcCIVACAsm - Clean and Invalidate Data Cache by VA to PoC
// RISC-V: No S-mode cache control instructions — hardware handles coherence
// This is a no-op on RISC-V
TEXT ·dcCIVACAsm(SB), NOSPLIT, $0-8
	RET

// dcCVAUAsm - Clean Data Cache by VA to Point of Unification
// RISC-V: No S-mode cache control instructions
TEXT ·dcCVAUAsm(SB), NOSPLIT, $0-8
	RET

// icIVAUAsm - Invalidate Instruction Cache by VA
// RISC-V: FENCE.I is global (no per-VA invalidation in S-mode)
TEXT ·icIVAUAsm(SB), NOSPLIT, $0-8
	// FENCE.I = 0x0000100F
	WORD	$0x0000100F
	RET

// icIALLUAsm - Invalidate ALL Instruction Cache
// RISC-V: FENCE.I
TEXT ·icIALLUAsm(SB), NOSPLIT, $0-0
	// FENCE.I = 0x0000100F
	WORD	$0x0000100F
	RET

// dcZVAAsm - Zero a single cache line
// RISC-V: Software zeroing (no DC ZVA equivalent)
// Assume 64-byte cache lines
TEXT ·dcZVAAsm(SB), NOSPLIT, $0-8
	MOV	addr+0(FP), A0
	MOV	ZERO, 0(A0)
	MOV	ZERO, 8(A0)
	MOV	ZERO, 16(A0)
	MOV	ZERO, 24(A0)
	MOV	ZERO, 32(A0)
	MOV	ZERO, 40(A0)
	MOV	ZERO, 48(A0)
	MOV	ZERO, 56(A0)
	RET

// bzero4KAsm - Zero a 4KB page
// RISC-V: Software loop (no hardware page zeroing)
// 4096 / 8 = 512 doublewords
TEXT ·bzero4KAsm(SB), NOSPLIT, $0-8
	MOV	ptr+0(FP), A0
	MOV	$512, A1	// 512 doublewords
bzero_loop:
	MOV	ZERO, 0(A0)
	ADD	$8, A0
	ADD	$-1, A1
	BNE	A1, ZERO, bzero_loop
	RET

// writeTTBR0Asm - Write page table base register
// RISC-V: Write SATP
TEXT ·writeTTBR0Asm(SB), NOSPLIT, $0-8
	// DEBUG: Print '!' marker whenever SATP is written
	MOV	$0xFFFFFFFF10000000, A1
	MOV	$0x21, A2		// '!'
	MOVB	A2, (A1)
	MOV	val+0(FP), A0
	// CSRW satp, A0 (satp = 0x180)
	WORD	$0x18051073
	// SFENCE.VMA to ensure TLB sees new page tables
	WORD	$0x12000073
	RET

// atS1E0R - Address Translation Stage 1 EL0 Read
// RISC-V: No direct hardware page table walk instruction in S-mode
// Return 1 (fault indicator) — software must walk page tables
TEXT ·atS1E0R(SB), NOSPLIT, $0-16
	MOV	$1, A0
	MOV	A0, ret+8(FP)	// Return fault indicator
	RET

// dcCleanInvalidateAll - Clean and Invalidate entire D-cache
// RISC-V: No S-mode cache flush instructions — hardware handles coherence
// This is a no-op on RISC-V
TEXT ·dcCleanInvalidateAll(SB), NOSPLIT, $0-0
	RET
