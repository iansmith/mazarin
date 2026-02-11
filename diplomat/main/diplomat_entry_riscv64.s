//go:build linux && riscv64

// DIPLOMAT REAL ENTRY: Main entry point for RISC-V diplomat
//
// Called by trampoline_riscv64.s which receives OpenSBI parameters.
// OpenSBI parameters already saved in S0 (hart ID) and S1 (FDT address).

#include "textflag.h"

// UART NS16550A register addresses (physical)
#define UART_BASE   0x10000000
#define UART_THR    0
#define UART_LSR    5
#define UART_LSR_THRE 0x20

// Sv48 mode for SATP
#define SATP_MODE_SV48  0x9000000000000000

// PTE flag bits
#define PTE_V    0x01
#define PTE_R    0x02
#define PTE_W    0x04
#define PTE_X    0x08
#define PTE_G    0x20
#define PTE_A    0x40
#define PTE_D    0x80

// Kernel linear map offset
#define KERNEL_VA_OFFSET  0xFFFFFFFF00000000

// Memory layout
#define PT_POOL_BASE     0x81200000
#define G0_STACK_BASE    0x81210000
#define G0_STACK_SIZE    0x8000
#define EXC_STACK_BASE   0x81218000
#define EXC_STACK_SIZE   0x4000
#define BUMP_ALLOC_START 0x81220000

// g struct offsets
#define g_stack_lo    0
#define g_stack_hi    8
#define g_stackguard0 16
#define g_stackguard1 24

// diplomatRealEntry is called by the trampoline with OpenSBI params in S0/S1.
TEXT ·diplomatRealEntry(SB),NOSPLIT|NOFRAME,$0
	// ---- Print 'D' to UART ----
	MOV	$UART_BASE, T0
uart_wait_1:
	MOVBU	5(T0), T1
	AND	$UART_LSR_THRE, T1
	BEQ	T1, ZERO, uart_wait_1
	MOV	$'D', T1
	MOVB	T1, (T0)

	// ---- Set up stack ----
	MOV	$(G0_STACK_BASE + G0_STACK_SIZE), SP

	// ---- Zero page table pool ----
	MOV	$PT_POOL_BASE, T0
	MOV	$(PT_POOL_BASE + 16*4096), T1
zero_pt_loop:
	MOV	ZERO, (T0)
	ADD	$8, T0
	BNE	T0, T1, zero_pt_loop

	// ---- Build Sv48 page tables ----
	MOV	$PT_POOL_BASE, S2

	// L3[0] → L2 identity table
	MOV	$(PT_POOL_BASE + 0x1000), T0
	SRL	$12, T0, T0
	SLL	$10, T0, T0
	OR	$PTE_V, T0, T0
	MOV	$PT_POOL_BASE, T1
	MOV	T0, (T1)

	// L3[511] → L2 kernel table
	MOV	$(PT_POOL_BASE + 0x2000), T0
	SRL	$12, T0, T0
	SLL	$10, T0, T0
	OR	$PTE_V, T0, T0
	MOV	$PT_POOL_BASE, T1
	ADD	$(511 * 8), T1
	MOV	T0, (T1)

	// L2[2] identity: 0x80000000 (1GB)
	MOV	$0x80000, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x1000), T1
	ADD	$(2 * 8), T1
	MOV	T0, (T1)

	// L2[0] identity: 0x00000000 (MMIO)
	MOV	$0, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x1000), T1
	MOV	T0, (T1)

	// L2[0x1FC] kernel: PA 0x00000000
	MOV	$0, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FC * 8), T1
	MOV	T0, (T1)

	// L2[0x1FD] kernel: PA 0x40000000
	MOV	$0x40000, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FD * 8), T1
	MOV	T0, (T1)

	// L2[0x1FE] kernel: PA 0x80000000
	MOV	$0x80000, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FE * 8), T1
	MOV	T0, (T1)

	// L2[0x1FF] kernel: PA 0xC0000000
	MOV	$0xC0000, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FF * 8), T1
	MOV	T0, (T1)

	// ---- Enable MMU ----
	MOV	S2, T0
	SRL	$12, T0, T0
	MOV	$SATP_MODE_SV48, T1
	OR	T0, T1, T0
	WORD	$0x18029073		// csrw satp, t0
	WORD	$0x12000073		// sfence.vma

	// ---- Print 'P' via high-VA UART ----
	MOV	$0xFFFFFFFF10000000, T0
uart_wait_2:
	MOVBU	5(T0), T1
	AND	$UART_LSR_THRE, T1
	BEQ	T1, ZERO, uart_wait_2
	MOV	$'P', T1
	MOVB	T1, (T0)

	// ---- Convert SP to high VA ----
	MOV	$KERNEL_VA_OFFSET, T0
	ADD	T0, SP, SP

	// ---- Store globals ----
	MOV	$main·savedHartID(SB), T0
	MOV	S0, (T0)
	MOV	$main·savedFDTAddr(SB), T0
	MOV	S1, (T0)
	MOV	$main·ptPoolBase(SB), T0
	MOV	$PT_POOL_BASE, T1
	MOV	T1, (T0)
	MOV	$main·bumpAllocStart(SB), T0
	MOV	$BUMP_ALLOC_START, T1
	MOV	T1, (T0)
	MOV	$main·g0StackPA(SB), T0
	MOV	$G0_STACK_BASE, T1
	MOV	T1, (T0)
	MOV	$main·excStackPA(SB), T0
	MOV	$EXC_STACK_BASE, T1
	MOV	T1, (T0)

	// ---- Initialize g0 ----
	MOV	$runtime·g0(SB), g

	// Set g0.stack bounds
	MOV	$G0_STACK_BASE, T0
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	MOV	T0, g_stack_lo(g)
	MOV	$(G0_STACK_BASE + G0_STACK_SIZE), T0
	ADD	T1, T0, T0
	MOV	T0, g_stack_hi(g)

	// Stack guards
	MOV	$(G0_STACK_BASE + 1024), T0
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	MOV	T0, g_stackguard0(g)
	MOV	T0, g_stackguard1(g)

	// Link g0 and m0
	MOV	$runtime·m0(SB), T0
	MOV	g, 48(T0)
	MOV	T0, 48(g)

	// ---- Set up TLS ----
	MOV	$runtime·tls_g(SB), T0
	MOV	g, (T0)
	ADD	$8, T0
	MOV	T0, TP

	// ---- Store exception stack in SSCRATCH ----
	MOV	$(EXC_STACK_BASE + EXC_STACK_SIZE), T0
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	WORD	$0x14029073		// csrw sscratch, t0

	// ---- Call DiplomatEntry ----
	CALL	main·DiplomatEntry(SB)

halt:
	WORD	$0x10500073		// wfi
	JMP	halt
