// diplomat/main/pagetable_riscv64.s
// RISC-V 64-bit page table and control register operations
//
// RISC-V uses SATP (Supervisor Address Translation and Protection) CSR
// instead of x86's CR3 or ARM64's TTBR0.
// TLB invalidation uses SFENCE.VMA instruction.

#include "textflag.h"

// readSATP reads SATP CSR (supervisor address translation and protection)
// Go: func readSATP() uint64
TEXT ·readSATP(SB), NOSPLIT, $0-8
	// CSRR A0, SATP (read satp CSR to A0)
	WORD	$0x180022F3	// csrr a0, satp (satp = 0x180)
	MOV	A0, ret+0(FP)
	RET

// writeSATP writes to SATP CSR
// Go: func writeSATP(val uint64)
TEXT ·writeSATP(SB), NOSPLIT, $0-8
	MOV	val+0(FP), A0
	// CSRW SATP, A0 (write A0 to satp CSR)
	WORD	$0x18051073	// csrw satp, a0
	// SFENCE.VMA - flush TLB
	WORD	$0x12000073	// sfence.vma zero, zero (flush all TLBs)
	RET

// flushTLB invalidates all TLB entries using SFENCE.VMA
// Go: func flushTLB()
TEXT ·flushTLB(SB), NOSPLIT, $0-0
	// SFENCE.VMA zero, zero - invalidate all TLB entries
	WORD	$0x12000073	// sfence.vma
	RET

// flushTLBVA invalidates TLB entry for a specific virtual address
// Go: func flushTLBVA(va uint64)
TEXT ·flushTLBVA(SB), NOSPLIT, $0-8
	MOV	va+0(FP), A0
	// SFENCE.VMA A0, zero - invalidate TLB for VA in A0
	WORD	$0x12050073	// sfence.vma a0, zero
	RET

// readCycle reads the CYCLE CSR (cycle counter for pseudorandom seeds)
// Go: func readCycle() uint64
TEXT ·readCycle(SB), NOSPLIT, $0-8
	// RDCYCLE A0 (CSRR A0, CYCLE)
	WORD	$0xC00022F3	// csrr a0, cycle
	MOV	A0, ret+0(FP)
	RET

// readTime reads the TIME CSR (real-time counter)
// Go: func readTime() uint64
TEXT ·readTime(SB), NOSPLIT, $0-8
	// RDTIME A0 (CSRR A0, TIME)
	WORD	$0xC01022F3	// csrr a0, time
	MOV	A0, ret+0(FP)
	RET

// readTimerCounter reads the TIME CSR (used for pseudorandom seed generation)
// Go: func readTimerCounter() uint64
TEXT ·readTimerCounter(SB), NOSPLIT, $0-8
	// RDTIME A0
	WORD	$0xC01022F3	// csrr a0, time
	MOV	A0, ret+0(FP)
	RET

// jumpToEntry jumps to a kernel entry point. Does not return.
// Go: func jumpToEntry(entry uint64)
TEXT ·jumpToEntry(SB), NOSPLIT, $0-8
	MOV	entry+0(FP), A0

	// Clear registers for clean state
	MOV	ZERO, T0
	MOV	ZERO, T1
	MOV	ZERO, T2
	MOV	ZERO, S0
	MOV	ZERO, S1
	// A0 has entry point
	MOV	ZERO, A1
	MOV	ZERO, A2
	MOV	ZERO, A3
	MOV	ZERO, A4
	MOV	ZERO, A5
	MOV	ZERO, A6
	MOV	ZERO, A7
	// Skip clearing S2-S11 (X18-X27) - not recognized by Go assembler
	MOV	ZERO, T3
	MOV	ZERO, T4
	MOV	ZERO, T5
	MOV	ZERO, T6

	// Jump to entry point (no return)
	JMP	(A0)

// jumpToKmazarinWithStack sets up SP and SSCRATCH (for exception stack),
// installs kmazarin's exception handler (STVEC), then jumps to the kernel
// entry point in Supervisor mode.
//
// Go: func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, stvec uint64)
//
// entry:       kernel entry point (virtual address)
// g0StackPtr:  SP value pointing to argc on g0 stack (VA)
// excStackTop: exception stack top (VA) - stored in SSCRATCH
// stvec:       kmazarin's exception vector base (VA for STVEC CSR)
TEXT ·jumpToKmazarinWithStack(SB), NOSPLIT, $0-32
	MOV	entry+0(FP), A0
	MOV	g0StackPtr+8(FP), A1	// g0 SP value
	MOV	excStackTop+16(FP), A2	// exception stack top
	MOV	stvec+24(FP), A3	// STVEC value

	// Set SP to g0 stack pointer (pointing to argc)
	MOV	A1, SP

	// Store exception stack top in SSCRATCH CSR
	// RISC-V convention: exception handler swaps SP with SSCRATCH
	WORD	$0x14061073		// csrw sscratch, a2 (sscratch = 0x140)

	// Clear SSTATUS SUM bit (Supervisor User Memory access)
	// and disable interrupts (SIE bit)
	// Read SSTATUS
	WORD	$0x100022F3		// csrr a0, sstatus (sstatus = 0x100)
	// Clear bit 18 (SUM) and bit 1 (SIE)
	MOV	$0x40002, T0		// bits to clear: 18 (SUM) and 1 (SIE)
	NOT	T0, T0			// T0 = ~T0
	AND	T0, A0, A0		// A0 = A0 & ~(SUM | SIE)
	WORD	$0x10051073		// csrw sstatus, a0

	// Install kmazarin's exception handler (STVEC).
	// STVEC[1:0] = mode (0 = Direct, 1 = Vectored)
	// We use Direct mode (all exceptions go to BASE address)
	// Clear low 2 bits to ensure Direct mode
	AND	$-4, A3, A3		// A3 = A3 & ~0x3
	WORD	$0x10569073		// csrw stvec, a3 (stvec = 0x105)

	// Clear SIP (Supervisor Interrupt Pending) to prevent spurious interrupts
	WORD	$0x14451073		// csrw sip, zero (sip = 0x144)

	// Clear registers for clean state
	MOV	ZERO, T0
	MOV	ZERO, T1
	MOV	ZERO, T2
	MOV	ZERO, S0
	MOV	ZERO, S1
	// A0 has entry point
	MOV	ZERO, A1
	MOV	ZERO, A2
	MOV	ZERO, A3
	MOV	ZERO, A4
	MOV	ZERO, A5
	MOV	ZERO, A6
	MOV	ZERO, A7
	// Skip clearing S2-S11 (X18-X27) - not recognized by Go assembler
	MOV	ZERO, T3
	MOV	ZERO, T4
	MOV	ZERO, T5
	MOV	ZERO, T6

	// Jump to kmazarin entry point (_rt0_riscv64_linux)
	JMP	(A0)

// disableWriteProtect is a no-op on RISC-V in Supervisor mode
// (no equivalent to x86's CR0.WP bit)
// Go: func disableWriteProtect()
TEXT ·disableWriteProtect(SB), NOSPLIT, $0-0
	RET

// enableWriteProtect is a no-op on RISC-V in Supervisor mode
// Go: func enableWriteProtect()
TEXT ·enableWriteProtect(SB), NOSPLIT, $0-0
	RET
