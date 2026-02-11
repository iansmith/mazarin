//go:build riscv64

// diplomat/main/entry_riscv64.s
// RISC-V 64-bit entry point — entered from OpenSBI in S-mode
//
// OpenSBI provides:
//   A0 = hart ID (always 0 for primary boot hart)
//   A1 = FDT (Flattened Device Tree) physical address
//   SATP = 0 (bare mode, no page tables)
//   SIE = 0 (interrupts disabled)
//   SP = OpenSBI-provided stack (unreliable size)
//
// This file:
//   1. Saves A0/A1 to s-registers
//   2. Prints "D" to UART (proof of life)
//   3. Sets up diplomat's stack in known RAM
//   4. Builds Sv48 page tables
//   5. Enables MMU (csrw satp)
//   6. Jumps to high-memory Go entry

#include "textflag.h"

// UART NS16550A register addresses (physical)
#define UART_BASE   0x10000000
#define UART_THR    0           // Transmit Holding Register
#define UART_LSR    5           // Line Status Register
#define UART_LSR_THRE 0x20     // Transmitter Holding Register Empty

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

// diplomat binary is loaded at 0x80200000 by OpenSBI (-kernel flag)
// Stack and page tables go after the binary. Assume diplomat is < 16MB.
// Reserve space starting at 0x81200000:
//   PT pool:   0x81200000 - 0x8120FFFF (16 pages = 64KB for page tables)
//   G0 stack:  0x81210000 - 0x81217FFF (8 pages = 32KB)
//   Exc stack: 0x81218000 - 0x8121BFFF (4 pages = 16KB)
// These MUST be below the bump allocator start address.

#define PT_POOL_BASE     0x81200000
#define G0_STACK_BASE    0x81210000
#define G0_STACK_SIZE    0x8000       // 32KB
#define EXC_STACK_BASE   0x81218000
#define EXC_STACK_SIZE   0x4000       // 16KB
#define BUMP_ALLOC_START 0x81220000   // bump allocator starts here

// Offsets into Go's g struct (must match Go runtime)
#define g_stack_lo    0
#define g_stack_hi    8
#define g_stackguard0 16
#define g_stackguard1 24

TEXT ·diplomatEntry(SB), NOSPLIT|NOFRAME, $0
	// Save OpenSBI parameters
	MOV	A0, S0		// S0 = hart ID
	MOV	A1, S1		// S1 = FDT physical address

	// ---- Step 1: Print 'D' to UART (proof of life) ----
	MOV	$UART_BASE, T0
uart_wait_1:
	MOVBU	5(T0), T1	// Read LSR
	AND	$UART_LSR_THRE, T1
	BEQ	T1, ZERO, uart_wait_1
	MOV	$'D', T1
	MOVB	T1, (T0)	// Write 'D' to THR

	// ---- Step 2: Set up our own stack ----
	// Use g0 stack top as initial SP (physical address, no MMU yet)
	MOV	$(G0_STACK_BASE + G0_STACK_SIZE), SP

	// ---- Step 3: Zero page table pool (64KB) ----
	MOV	$PT_POOL_BASE, T0
	MOV	$(PT_POOL_BASE + 16*4096), T1  // 16 pages
zero_pt_loop:
	MOV	ZERO, (T0)
	ADD	$8, T0
	BNE	T0, T1, zero_pt_loop

	// ---- Step 4: Build Sv48 page tables ----
	// Allocate pages from PT pool:
	//   Page 0 (0x81200000): L3 root table
	//   Page 1 (0x81201000): L2 table for identity map (low addresses)
	//   Page 2 (0x81202000): L2 table for kernel high VA (0xFFFFFFFF...)

	// --- L3 root (page 0) ---
	MOV	$PT_POOL_BASE, S2	// S2 = L3 root PA

	// L3 entry for identity map: VA 0x80000000
	// VA[47:39] of 0x80000000: upper bits are 0 → L3 index = 0
	// Points to L2 table (page 1)
	MOV	$(PT_POOL_BASE + 0x1000), T0   // L2 table for identity map
	SRL	$12, T0, T0		// PPN
	SLL	$10, T0, T0		// shift to PTE position
	OR	$PTE_V, T0, T0		// Branch PTE (V only, no R/W/X)
	MOV	$PT_POOL_BASE, T1
	MOV	T0, (T1)		// L3[0]

	// L3 entry for kernel VA: VA 0xFFFFFFFF00000000
	// Sv48 VA[47:39] = sign-extended bits[47:39]
	// For 0xFFFFFFFF00000000, bit 47 is 1, bits 46:39 are all 1
	// → L3 index = 511 (0x1FF)
	// Points to L2 table (page 2)
	MOV	$(PT_POOL_BASE + 0x2000), T0   // L2 table for kernel
	SRL	$12, T0, T0
	SLL	$10, T0, T0
	OR	$PTE_V, T0, T0
	MOV	$PT_POOL_BASE, T1
	ADD	$(511 * 8), T1		// L3[511]
	MOV	T0, (T1)

	// --- L2 table for identity map (page 1) ---
	// Map 0x80000000 as 1GB gigapage leaf
	// VA 0x80000000: L2 index = (0x80000000 >> 30) & 0x1FF = 2
	// PA = 0x80000000, PPN = 0x80000000 >> 12 = 0x80000
	MOV	$0x80000, T0		// PPN of 0x80000000
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x1000), T1	// L2 identity table
	ADD	$(2 * 8), T1		// L2[2]
	MOV	T0, (T1)

	// Also identity-map 0x00000000-0x3FFFFFFF (1GB) for MMIO (UART at 0x10000000)
	// L2[0]: PA=0, 1GB gigapage
	MOV	$0, T0			// PPN of 0x00000000
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x1000), T1
	MOV	T0, (T1)		// L2[0]

	// --- L2 table for kernel VA (page 2) ---
	// Linear map: VA 0xFFFFFFFF00000000 + PA → PA
	// L2 index for VA 0xFFFFFFFF00000000: VA[38:30]
	// 0xFFFFFFFF00000000 >> 30 = 0x3FFFFFFFC0 (only low 9 bits matter)
	// bits[38:30] of 0xFFFFFFFF00000000: 0b111111100 = 0x1FC
	// So L2[0x1FC] maps PA 0x00000000 (1GB, MMIO region)

	// L2[0x1FC]: PA 0x00000000 (UART, PLIC, CLINT)
	MOV	$0, T0
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FC * 8), T1
	MOV	T0, (T1)

	// L2[0x1FD]: PA 0x40000000 (PCI MMIO window)
	MOV	$0x40000, T0		// PPN of 0x40000000
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_A | PTE_D), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FD * 8), T1
	MOV	T0, (T1)

	// L2[0x1FE]: PA 0x80000000 (RAM)
	MOV	$0x80000, T0		// PPN of 0x80000000
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FE * 8), T1
	MOV	T0, (T1)

	// L2[0x1FF]: PA 0xC0000000 (RAM upper 1GB)
	MOV	$0xC0000, T0		// PPN of 0xC0000000
	SLL	$10, T0, T0
	MOV	$(PTE_V | PTE_R | PTE_W | PTE_X | PTE_A | PTE_D | PTE_G), T2
	OR	T2, T0, T0
	MOV	$(PT_POOL_BASE + 0x2000), T1
	ADD	$(0x1FF * 8), T1
	MOV	T0, (T1)

	// ---- Step 5: Enable MMU ----
	// SATP = (MODE=9 << 60) | (PPN of L3 root)
	MOV	S2, T0			// L3 root PA
	SRL	$12, T0, T0		// PPN
	MOV	$SATP_MODE_SV48, T1
	OR	T0, T1, T0
	// csrw satp, t0 (satp = CSR 0x180, t0 = x5)
	// Encoding: csrw satp, t0 = csrrw x0, 0x180, x5
	// imm[11:0]=0x180, rs1=x5, funct3=001, rd=x0, opcode=1110011
	WORD	$0x18029073		// csrw satp, t0
	// sfence.vma (flush TLB)
	WORD	$0x12000073		// sfence.vma zero, zero

	// ---- Step 6: Print 'P' via high-VA UART (verify MMU works) ----
	MOV	$0xFFFFFFFF10000000, T0	// UART via linear map
uart_wait_2:
	MOVBU	5(T0), T1
	AND	$UART_LSR_THRE, T1
	BEQ	T1, ZERO, uart_wait_2
	MOV	$'P', T1
	MOVB	T1, (T0)

	// ---- Step 7: Jump to high-memory Go bootstrap ----
	// Convert SP to high VA
	MOV	$KERNEL_VA_OFFSET, T0
	ADD	T0, SP, SP

	// Store hart ID and FDT address as globals (via high VA)
	MOV	$·savedHartID(SB), T0
	MOV	S0, (T0)
	MOV	$·savedFDTAddr(SB), T0
	MOV	S1, (T0)

	// Store physical addresses of key regions for Go code
	MOV	$·ptPoolBase(SB), T0
	MOV	$PT_POOL_BASE, T1
	MOV	T1, (T0)

	MOV	$·bumpAllocStart(SB), T0
	MOV	$BUMP_ALLOC_START, T1
	MOV	T1, (T0)

	MOV	$·g0StackPA(SB), T0
	MOV	$G0_STACK_BASE, T1
	MOV	T1, (T0)

	MOV	$·excStackPA(SB), T0
	MOV	$EXC_STACK_BASE, T1
	MOV	T1, (T0)

	// Initialize g0 and TLS
	// g0 is a Go global — its address is known at link time
	MOV	$runtime·g0(SB), g	// g = X26 (Go's g register on RISC-V)

	// Set g0.stack bounds (using high-VA addresses)
	MOV	$G0_STACK_BASE, T0
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	MOV	T0, g_stack_lo(g)	// g0.stack.lo
	MOV	$(G0_STACK_BASE + G0_STACK_SIZE), T0
	ADD	T1, T0, T0
	MOV	T0, g_stack_hi(g)	// g0.stack.hi

	// Stack guards
	MOV	$(G0_STACK_BASE + 1024), T0	// guard = lo + 1024
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	MOV	T0, g_stackguard0(g)
	MOV	T0, g_stackguard1(g)

	// Link g0 and m0
	MOV	$runtime·m0(SB), T0
	MOV	g, 48(T0)		// m0.g0 = &g0 (offset 48)
	MOV	T0, 48(g)		// g0.m = &m0 (offset 48)

	// Set up TLS via TP register
	// Go expects g at [TP - 8]
	MOV	$runtime·tls_g(SB), T0
	MOV	g, (T0)			// Store g0 at tls_g[0]
	ADD	$8, T0			// TP = &tls_g + 8
	MOV	T0, TP			// TP register (X4)

	// Store exception stack top in SSCRATCH for trap handler
	MOV	$(EXC_STACK_BASE + EXC_STACK_SIZE), T0
	MOV	$KERNEL_VA_OFFSET, T1
	ADD	T1, T0, T0
	// csrw sscratch, t0 (sscratch = CSR 0x140, t0 = x5)
	WORD	$0x14029073		// csrw sscratch, t0

	// Call Go DiplomatEntry
	CALL	·DiplomatEntry(SB)

	// Should not return
halt:
	WORD	$0x10500073		// wfi
	JMP	halt
