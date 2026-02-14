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

// jumpToKmazarinWithStack switches to kernel Sv48 page tables, sets up SP
// and SSCRATCH (for exception stack), installs kmazarin's exception handler
// (STVEC), then jumps to the kernel entry point in Supervisor mode.
//
// The SATP value is read from the package-level global kernelSAPTVal, set
// during PrepareKernelVMRISCV. The identity map in the kernel page tables
// ensures diplomat's code keeps running after the SATP switch.
//
// Go: func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, stvec uint64)
//
// entry:       kernel entry point (virtual address)
// g0StackPtr:  SP value pointing to argc on g0 stack (VA)
// excStackTop: exception stack top (VA) - stored in SSCRATCH
// stvec:       kmazarin's exception vector base (VA for STVEC CSR)
TEXT ·jumpToKmazarinWithStack(SB), NOSPLIT, $0-32
	MOV	entry+0(FP), S0		// S0 = entry point (preserved across steps)
	MOV	g0StackPtr+8(FP), A1	// g0 SP value
	MOV	excStackTop+16(FP), A2	// exception stack top
	MOV	stvec+24(FP), A3	// STVEC value

	// Step 1: Switch SATP to kernel page tables.
	// Identity map in kernel tables keeps diplomat code at ~0x80200000 mapped.
	MOV	·kernelSAPTVal(SB), T0
	WORD	$0x18029073		// csrw satp, t0 (satp = 0x180, t0 = x5)
	WORD	$0x12000073		// sfence.vma zero, zero (flush all TLBs)

	// Step 2: Now kernel VAs are active. Set SP to g0 stack (kernel VA).
	// After this, FP-relative addressing into diplomat's old stack frame is invalid.
	MOV	A1, SP

	// Step 3: Store exception stack top in SSCRATCH CSR
	// RISC-V convention: exception handler swaps SP with SSCRATCH
	WORD	$0x14061073		// csrw sscratch, a2 (sscratch = 0x140)

	// Step 4: Configure sstatus: clear SUM+SIE, enable FPU (FS=01 Initial)
	WORD	$0x100024F3		// csrr s1, sstatus (sstatus = 0x100, s1 = x9)
	MOV	$0x46002, T0		// clear: bit 18 (SUM), bits 14:13 (FS), bit 1 (SIE)
	NOT	T0, T0			// T0 = ~T0
	AND	T0, S1, S1		// S1 = S1 & ~(SUM | FS | SIE)
	MOV	$0x2000, T0		// FS = 01 (Initial): enable FP instructions
	OR	T0, S1, S1
	WORD	$0x10049073		// csrw sstatus, s1

	// Step 5: Install kmazarin's exception handler (STVEC).
	// STVEC[1:0] = mode (0 = Direct, 1 = Vectored)
	// We use Direct mode (all exceptions go to BASE address)
	AND	$-4, A3, A3		// A3 = A3 & ~0x3
	WORD	$0x10569073		// csrw stvec, a3 (stvec = 0x105)

	// Step 6: Clear SIP (Supervisor Interrupt Pending) to prevent spurious interrupts
	WORD	$0x14451073		// csrw sip, zero (sip = 0x144)

	// Step 6.5: Remove identity maps from kernel page tables.
	// The identity maps (L2[0]=0x00000000, L2[2]=0x80000000) were only needed
	// for the SATP transition trampoline. Clear them so the kernel faults on
	// any access to low (non-high-memory) virtual addresses.
	// No SFENCE: stale TLB entries keep current code running; future misses fault.
	WORD	$0x180022F3		// csrr t0(x5), satp
	SLL	$20, T0, T0		// clear mode (4 bits) + ASID (16 bits)
	SRL	$8, T0, T0		// T0 = PPN << 12 = L3 root phys addr
	MOV	(T0), T1		// T1 = L3[0] PTE (branch to L2 table)
	SRL	$10, T1, T1
	SLL	$12, T1, T1		// T1 = L2 table phys addr
	MOV	ZERO, 0(T1)		// Clear L2[0]: 0x00000000 MMIO identity map
	MOV	ZERO, 16(T1)		// Clear L2[2]: 0x80000000 RAM identity map

	// Step 7: Move entry point to A0 for the jump
	MOV	S0, A0

	// Step 8: Clear registers for clean state
	MOV	ZERO, T0
	MOV	ZERO, T1
	MOV	ZERO, T2
	MOV	ZERO, S0
	MOV	ZERO, S1
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
	// Set TP (X4) = hart ID (0). Diplomat left TP pointing at its TLS block,
	// but kmazarin uses TP as hart ID for per-CPU data indexing (getCPUIDAsm).
	// Without this, GetCPUID() returns garbage → per-CPU queue mismatch →
	// threads enqueued to CPU 0 but dequeued from CPU 15 → infinite yield loop.
	MOV	ZERO, TP

	// Step 9: Jump to kmazarin entry point (_rt0_riscv64_linux)
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
