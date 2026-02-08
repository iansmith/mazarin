//go:build amd64

// diplomat/main/exc_vectors_amd64.s
// Page fault handler for demand paging during kmazarin's Go runtime init.
//
// This handler runs BEFORE the Go runtime is fully initialized in kmazarin.
// It must be pure assembly — no Go function calls.
//
// The handler services #PF (page fault) exceptions for the kernel heap VA range
// (0xFFFF000100000000 - 0xFFFF100000000000) by walking the PML4 page tables
// and mapping new 4KB pages from a pre-allocated pool.
//
// x86_64 4-level paging:
//   PML4 (L0): index = (VA >> 39) & 0x1FF
//   PDPT (L1): index = (VA >> 30) & 0x1FF
//   PD   (L2): index = (VA >> 21) & 0x1FF
//   PT   (L3): index = (VA >> 12) & 0x1FF
//
// Page table entry flags:
//   PTE_PRESENT  = 1 << 0
//   PTE_WRITABLE = 1 << 1

#include "textflag.h"

// demandPagePool layout (must match kernelvm_amd64.go):
//   +0:  current   (next available page PA)
//   +8:  end       (end of page pool)
//   +16: ptCurrent (next available PT page PA)
//   +24: ptEnd     (end of PT pool)
//   +32: pml4Phys  (PML4 physical address)
//   +40: pgCount   (page fault counter)

// getDiplomatPFHandlerAddr returns the address of the page fault handler stub.
// func getDiplomatPFHandlerAddr() uint64
TEXT ·getDiplomatPFHandlerAddr(SB), NOSPLIT, $0-8
	LEAQ	diplomatPageFaultHandler(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// installIDT loads the IDT register via LIDT.
// func installIDT(idtrAddr uint64)
TEXT ·installIDT(SB), NOSPLIT, $0-8
	MOVQ	idtrAddr+0(FP), AX
	BYTE	$0x0F; BYTE $0x01; BYTE $0x18	// LIDT [RAX]
	RET

// disableInterrupts clears the interrupt flag (CLI).
// func disableInterrupts()
TEXT ·disableInterrupts(SB), NOSPLIT, $0-0
	CLI
	RET

// readCSSelector returns the current CS segment selector.
// func readCSSelector() uint16
TEXT ·readCSSelector(SB), NOSPLIT, $0-2
	MOVW	CS, AX
	MOVW	AX, ret+0(FP)
	RET

// diplomatPageFaultHandler handles #PF exceptions for demand paging.
// On entry (CPU pushed): [error_code, RIP, CS, RFLAGS, RSP, SS]
//
// We need to:
// 1. Read CR2 (faulting address)
// 2. Check if it's in the heap VA range
// 3. Walk PML4 page tables, allocating levels as needed
// 4. Map a physical page
// 5. Return via IRETQ
TEXT diplomatPageFaultHandler(SB), NOSPLIT|NOFRAME, $0
	// Save registers we'll use
	PUSHQ	AX
	PUSHQ	BX
	PUSHQ	CX
	PUSHQ	DX
	PUSHQ	SI
	PUSHQ	DI
	PUSHQ	R8
	PUSHQ	R9

	// Disable CR0.WP so we can modify UEFI's read-only page table pages
	MOVQ	CR0, R9		// R9 = saved CR0
	MOVQ	R9, AX
	ANDQ	$~(1<<16), AX	// Clear WP bit (bit 16)
	MOVQ	AX, CR0

	// Read CR2 — faulting virtual address
	MOVQ	CR2, AX

	// Page-align: AX &= ~0xFFF
	ANDQ	$~0xFFF, AX
	MOVQ	AX, SI		// SI = page-aligned faulting VA

	// Check if VA is in heap range: 0xFFFF800100000000 <= VA < 0xFFFF900000000000
	// (x86_64 canonical addresses — ARM64 uses 0xFFFF0001... but that's non-canonical here)
	MOVQ	$0xFFFF800100000000, BX
	CMPQ	AX, BX
	JB	pf_halt		// Below heap start

	MOVQ	$0xFFFF900000000000, BX
	CMPQ	AX, BX
	JAE	pf_halt		// At or above heap end

	// Allocate a physical page from the pool
	LEAQ	·demandPagePool(SB), DI	// DI = &demandPagePool
	MOVQ	0(DI), AX		// current
	MOVQ	8(DI), BX		// end
	CMPQ	AX, BX
	JAE	pf_halt			// Pool exhausted

	// Bump pointer by 4KB
	LEAQ	4096(AX), CX
	MOVQ	CX, 0(DI)		// store new current
	// AX = new page PA
	MOVQ	AX, R8			// R8 = new page PA (save)

	// Walk page tables: PML4 → PDPT → PD → PT
	// Load PML4 physical address
	MOVQ	32(DI), AX		// pml4Phys

	// ---- PML4 (L0) ----
	// Index = (VA >> 39) & 0x1FF
	MOVQ	SI, BX
	SHRQ	$39, BX
	ANDQ	$0x1FF, BX
	SHLQ	$3, BX			// BX = index * 8
	ADDQ	BX, AX			// AX = &PML4[idx]
	MOVQ	(AX), CX		// CX = PML4 entry
	MOVQ	AX, DX			// DX = save entry addr for write-back

	TESTQ	$1, CX			// Present?
	JZ	alloc_pdpt

	ANDQ	$~0xFFF, CX		// CX = PDPT phys
	JMP	have_pdpt

alloc_pdpt:
	// Allocate PDPT from PT pool
	LEAQ	·demandPagePool(SB), DI
	MOVQ	16(DI), AX		// ptCurrent
	MOVQ	24(DI), BX		// ptEnd
	CMPQ	AX, BX
	JAE	pf_halt
	LEAQ	4096(AX), CX
	MOVQ	CX, 16(DI)		// bump

	// Zero the new page
	MOVQ	AX, CX
	MOVQ	$512, BX		// 512 qwords = 4KB
zero_pdpt:
	MOVQ	$0, (CX)
	ADDQ	$8, CX
	DECQ	BX
	JNZ	zero_pdpt

	// Write PML4 entry: PDPT phys | Present | Writable
	MOVQ	AX, CX			// CX = new PDPT phys
	ORQ	$3, AX			// Present + Writable
	MOVQ	AX, (DX)		// PML4[idx] = entry

have_pdpt:
	// CX = PDPT physical address
	// ---- PDPT (L1) ----
	// Index = (VA >> 30) & 0x1FF
	MOVQ	SI, BX
	SHRQ	$30, BX
	ANDQ	$0x1FF, BX
	SHLQ	$3, BX
	LEAQ	(CX)(BX*1), AX		// AX = &PDPT[idx]
	MOVQ	(AX), CX		// CX = PDPT entry
	MOVQ	AX, DX			// save entry addr

	TESTQ	$1, CX
	JZ	alloc_pd

	ANDQ	$~0xFFF, CX
	JMP	have_pd

alloc_pd:
	LEAQ	·demandPagePool(SB), DI
	MOVQ	16(DI), AX
	MOVQ	24(DI), BX
	CMPQ	AX, BX
	JAE	pf_halt
	LEAQ	4096(AX), CX
	MOVQ	CX, 16(DI)

	MOVQ	AX, CX
	MOVQ	$512, BX
zero_pd:
	MOVQ	$0, (CX)
	ADDQ	$8, CX
	DECQ	BX
	JNZ	zero_pd

	MOVQ	AX, CX
	ORQ	$3, AX
	MOVQ	AX, (DX)

have_pd:
	// CX = PD physical address
	// ---- PD (L2) ----
	// Index = (VA >> 21) & 0x1FF
	MOVQ	SI, BX
	SHRQ	$21, BX
	ANDQ	$0x1FF, BX
	SHLQ	$3, BX
	LEAQ	(CX)(BX*1), AX
	MOVQ	(AX), CX
	MOVQ	AX, DX

	TESTQ	$1, CX
	JZ	alloc_pt

	// Check if it's a 2MB page (PS bit = bit 7)
	TESTQ	$0x80, CX
	JNZ	pf_halt			// 2MB page — shouldn't happen for heap

	ANDQ	$~0xFFF, CX
	JMP	have_pt

alloc_pt:
	LEAQ	·demandPagePool(SB), DI
	MOVQ	16(DI), AX
	MOVQ	24(DI), BX
	CMPQ	AX, BX
	JAE	pf_halt
	LEAQ	4096(AX), CX
	MOVQ	CX, 16(DI)

	MOVQ	AX, CX
	MOVQ	$512, BX
zero_pt:
	MOVQ	$0, (CX)
	ADDQ	$8, CX
	DECQ	BX
	JNZ	zero_pt

	MOVQ	AX, CX
	ORQ	$3, AX
	MOVQ	AX, (DX)

have_pt:
	// CX = PT physical address
	// ---- PT (L3) ----
	// Index = (VA >> 12) & 0x1FF
	MOVQ	SI, BX
	SHRQ	$12, BX
	ANDQ	$0x1FF, BX
	SHLQ	$3, BX
	LEAQ	(CX)(BX*1), AX		// AX = &PT[idx]

	// Write PT entry: page PA | Present | Writable
	MOVQ	R8, CX			// R8 = new page PA
	ORQ	$3, CX			// Present + Writable
	MOVQ	CX, (AX)		// PT[idx] = page PA | flags

	// Flush TLB for the faulting address
	// INVLPG [SI] — invalidate TLB entry for the faulting page
	BYTE	$0x0F; BYTE $0x01; BYTE $0x3E	// INVLPG [RSI]

	// Zero the new 4KB page via its faulting VA (now mapped)
	// SI still holds the page-aligned faulting VA
	MOVQ	SI, DI
	MOVQ	$512, CX		// 512 qwords = 4KB
	XORQ	AX, AX
zero_new_page:
	MOVQ	AX, (DI)
	ADDQ	$8, DI
	DECQ	CX
	JNZ	zero_new_page

	// Heartbeat: write '.' to COM1
	MOVW	$0x3F8, DX
	MOVB	$'.', AX
	OUTB

	// Increment page fault counter
	LEAQ	·demandPagePool(SB), DI
	INCQ	40(DI)

	// Restore CR0 (re-enables WP)
	MOVQ	R9, CR0

	// Restore registers
	POPQ	R9
	POPQ	R8
	POPQ	DI
	POPQ	SI
	POPQ	DX
	POPQ	CX
	POPQ	BX
	POPQ	AX

	// Skip error code (pushed by CPU for #PF)
	ADDQ	$8, SP

	IRETQ

pf_halt:
	// Unhandled page fault — write "F!" to COM1 and halt
	MOVW	$0x3F8, DX
	MOVB	$'F', AX
	OUTB
	MOVB	$'!', AX
	OUTB
	HLT
	JMP	pf_halt

// getDiplomatSyscallHandlerAddr returns the address of the INT $0x80 stub handler.
// func getDiplomatSyscallHandlerAddr() uint64
TEXT ·getDiplomatSyscallHandlerAddr(SB), NOSPLIT, $0-8
	LEAQ	diplomatSyscallHandler(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// diplomatSyscallHandler handles INT $0x80 during Go runtime initialization.
//
// During early boot, before kmazarin installs its own IDT, the Go runtime
// may issue syscalls via Syscall6 (INT $0x80) or directly from runtime stubs.
// Most syscalls can be no-ops (prctl, epoll_wait, etc.) but clone MUST return
// a non-zero value to the parent, otherwise the parent takes the child code path.
//
// Syscall numbers use ARM64 Linux convention (kmazarin's internal standard):
//   SYS_clone = 220 (0xDC)
//   SYS_exit  = 93
//   SYS_gettid = 178
//
// INT $0x80 does NOT push an error code, so the stack on entry is:
//   [RIP, CS, RFLAGS, RSP, SS]
TEXT diplomatSyscallHandler(SB), NOSPLIT|NOFRAME, $0
	// Check for clone syscall — must NOT return 0 (parent thinks it's child)
	CMPQ	AX, $220	// SYS_clone (ARM64 numbering)
	JEQ	syscall_clone

	// Check for exit — halt instead of returning
	CMPQ	AX, $93		// SYS_exit
	JEQ	syscall_exit
	CMPQ	AX, $94		// SYS_exit_group
	JEQ	syscall_exit

	// Default: return 0 (success/no-op)
	XORQ	AX, AX
	IRETQ

syscall_clone:
	// Return a fake child TID (non-zero) so parent doesn't take child path.
	// The actual clone thread is never created during diplomat's boot stub.
	MOVQ	$42, AX		// fake TID
	IRETQ

syscall_exit:
	// Exit syscall — write 'X' to COM1 and halt
	MOVW	$0x3F8, DX
	MOVB	$'X', AX
	OUTB
syscall_exit_loop:
	HLT
	JMP	syscall_exit_loop
