//go:build arm64

// diplomat/main/exc_vectors_arm64.s
// Exception vector table and demand paging fault handler for diplomat.
//
// This handler runs during kmazarin's early init when Go's runtime is
// half-initialized. It must be pure assembly — no Go function calls.
//
// The handler services data abort faults in the kernel heap VA range
// (0xFFFF000100000000 - 0xFFFF100000000000) by mapping new 4KB pages
// from a pre-allocated pool.
//
// Kmazarin runs in EL1t mode (SPSel=0, using SP_EL0), so synchronous
// exceptions from kmazarin vector to the EL1t sync handler at offset 0x000.

#include "textflag.h"

// getDiplomatSyncEL1tAddr returns the address of diplomatSyncEL1t.
// Go: func getDiplomatSyncEL1tAddr() uint64
TEXT ·getDiplomatSyncEL1tAddr(SB), NOSPLIT, $0-8
	MOVD	$·diplomatSyncEL1t(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// getDiplomatSyncEL1hAddr returns the address of diplomatSyncEL1h.
// Go: func getDiplomatSyncEL1hAddr() uint64
TEXT ·getDiplomatSyncEL1hAddr(SB), NOSPLIT, $0-8
	MOVD	$·diplomatSyncEL1h(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// setVBAR sets VBAR_EL1 to the given address.
// Disables the GIC distributor and masks all DAIF exceptions before
// switching VBAR. UEFI's timer interrupts are active, and UEFI's ConOut
// may internally re-enable PSTATE.I. Without GIC disabled, a timer IRQ
// hitting our bare-ERET handler (no GIC EOI) loops forever.
// Go: func setVBAR(addr uint64)
TEXT ·setVBAR(SB), NOSPLIT, $0-8
	MSR	$0xF, DAIFSet	// mask Debug, SError, IRQ, FIQ
	ISB	$15
	// Disable GIC distributor — write 0 to GICD_CTLR at 0x08000000
	// This prevents any interrupt from reaching the CPU, even if
	// UEFI re-enables PSTATE.I during ConOut calls.
	MOVD	$0x08000000, R1
	MOVW	ZR, (R1)
	DSB	$15
	ISB	$15
	// Set VBAR_EL1
	MOVD	addr+0(FP), R0
	MSR	R0, VBAR_EL1
	ISB	$15
	RET

// diplomatSyncEL1t handles synchronous exceptions from EL1t (SP_EL0 mode).
// This is the demand paging handler.
TEXT ·diplomatSyncEL1t(SB), NOSPLIT|NOFRAME, $0
	// Switch to SP_EL1 for exception handling stack
	// MSR SPSel, #1 — use SP_EL1
	WORD	$0xD50041BF	// MSR SPSel, #1

	// Save registers on exception stack (SP_EL1)
	// STP x0, x1, [sp, #-16]!
	WORD	$0xA9BF07E0
	// STP x2, x3, [sp, #-16]!
	WORD	$0xA9BF0FE2
	// STP x4, x5, [sp, #-16]!
	WORD	$0xA9BF17E4
	// STP x6, x7, [sp, #-16]!
	WORD	$0xA9BF1FE6
	// STP x29, x30, [sp, #-16]!
	WORD	$0xA9BF7BFD

	// Read ESR_EL1 to check exception class
	MRS	ESR_EL1, R0

	// Extract EC (bits 31:26) — exception class
	LSR	$26, R0, R1

	// Check if EC == 0x15 (SVC from AArch64 — syscall)
	CMP	$0x15, R1
	BEQ	svc_handler_t

	// Check if EC == 0x25 (data abort from same EL)
	CMP	$0x25, R1
	BNE	halt_t

	// Read FAR_EL1 — faulting address
	MRS	FAR_EL1, R2

	// Page-align the faulting address (AND ~0xFFF)
	AND	$~0xFFF, R2, R2

	// Check if FAR is in heap range: 0xFFFF000100000000 <= FAR < 0xFFFF100000000000
	MOVD	$0xFFFF000100000000, R3
	CMP	R3, R2
	BLO	halt_t	// Below heap start

	MOVD	$0xFFFF100000000000, R3
	CMP	R3, R2
	BHS	halt_t	// At or above heap end

	// FAR is in heap range — allocate a page from pool
	// Load demandPagePool.current
	MOVD	$main·demandPagePool(SB), R3
	MOVD	(R3), R4		// current
	MOVD	8(R3), R5		// end

	// Check if pool exhausted
	CMP	R5, R4
	BHS	halt_t

	// Bump pointer by 4KB
	ADD	$4096, R4, R6
	MOVD	R6, (R3)		// store new current

	// R2 = faulting VA (page-aligned)
	// R4 = new page PA
	// Now walk TTBR1 page tables to install L3 entry

	// Read TTBR1_EL1 → L0 phys
	MRS	TTBR1_EL1, R0
	AND	$~0xFFF, R0, R0

	// L0 index = (VA >> 39) & 0x1FF
	LSR	$39, R2, R1
	AND	$0x1FF, R1, R1
	// L0 entry address
	LSL	$3, R1, R1
	ADD	R0, R1, R1		// R1 = &L0[idx]
	MOVD	(R1), R0		// R0 = L0 entry

	// Check L0 valid
	TST	$1, R0
	BEQ	alloc_l1_t

	AND	$~0xFFF, R0, R0	// L1 table phys
	B	have_l1_t

alloc_l1_t:
	// Allocate L1 from PT pool
	MOVD	$main·demandPagePool+16(SB), R5	// ptCurrent
	MOVD	(R5), R7
	MOVD	8(R5), R0		// ptEnd
	CMP	R0, R7
	BHS	halt_t
	ADD	$4096, R7, R0
	MOVD	R0, (R5)		// bump

	// Zero the new L1 page (R7 = page addr)
	MOVD	R7, R0
	MOVD	$512, R5		// 512 iterations × 8 bytes = 4KB
zero_l1_t:
	STP	(ZR, ZR), (R0)
	ADD	$16, R0, R0
	SUB	$2, R5, R5
	CBNZ	R5, zero_l1_t

	// Write L0 entry: L1 phys | 0x3 (table descriptor)
	ORR	$3, R7, R0
	MOVD	R0, (R1)		// L0[idx] = L1 | TABLE

	MOVD	R7, R0			// R0 = L1 phys

have_l1_t:
	// L1 index = (VA >> 30) & 0x1FF
	LSR	$30, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1		// R1 = &L1[idx]
	MOVD	(R1), R0		// R0 = L1 entry

	TST	$1, R0
	BEQ	alloc_l2_t

	AND	$~0xFFF, R0, R0	// L2 table phys
	B	have_l2_t

alloc_l2_t:
	// Allocate L2 from PT pool
	MOVD	$main·demandPagePool+16(SB), R5
	MOVD	(R5), R7
	MOVD	8(R5), R0
	CMP	R0, R7
	BHS	halt_t
	ADD	$4096, R7, R0
	MOVD	R0, (R5)

	MOVD	R7, R0
	MOVD	$512, R5
zero_l2_t:
	STP	(ZR, ZR), (R0)
	ADD	$16, R0, R0
	SUB	$2, R5, R5
	CBNZ	R5, zero_l2_t

	ORR	$3, R7, R0
	MOVD	R0, (R1)

	MOVD	R7, R0

have_l2_t:
	// L2 index = (VA >> 21) & 0x1FF
	LSR	$21, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1		// R1 = &L2[idx]
	MOVD	(R1), R0		// R0 = L2 entry

	TST	$1, R0
	BEQ	alloc_l3_t

	// Check if it's a block descriptor (bit 1 == 0 means block at L2)
	TST	$2, R0
	BEQ	halt_t	// Block descriptor — shouldn't happen for heap

	AND	$~0xFFF, R0, R0
	B	have_l3_t

alloc_l3_t:
	// Allocate L3 from PT pool
	MOVD	$main·demandPagePool+16(SB), R5
	MOVD	(R5), R7
	MOVD	8(R5), R0
	CMP	R0, R7
	BHS	halt_t
	ADD	$4096, R7, R0
	MOVD	R0, (R5)

	MOVD	R7, R0
	MOVD	$512, R5
zero_l3_t:
	STP	(ZR, ZR), (R0)
	ADD	$16, R0, R0
	SUB	$2, R5, R5
	CBNZ	R5, zero_l3_t

	ORR	$3, R7, R0
	MOVD	R0, (R1)

	MOVD	R7, R0

have_l3_t:
	// L3 index = (VA >> 12) & 0x1FF
	LSR	$12, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1		// R1 = &L3[idx]

	// Create L3 page entry: PA | ATTR_NORMAL_RW | DESC_PAGE
	// ATTR_NORMAL_RW = ATTR_IDX_NORMAL(3<<2) | AP_RW_EL1(0) | SH_INNER(3<<8) | AF(1<<10)
	// = 0xC | 0 | 0x300 | 0x400 = 0x70C
	// DESC_PAGE = 0x3
	// Entry = R4 | 0x70F
	MOVD	$0x70F, R5
	ORR	R5, R4, R0
	MOVD	R0, (R1)		// L3[idx] = page PA | attrs | PAGE

	// DSB + TLBI VALE1 + ISB
	DSB	$15
	// TLBI VAE1, X0 — invalidate by VA (R2 already page-aligned, shift right 12)
	LSR	$12, R2, R0
	// TLBI VAE1, X0
	// Encoding: sys #0, c8, c7, #1, x0
	WORD	$0xD5088720	// TLBI VAE1, X0
	DSB	$15
	ISB	$15

	// Zero the new 4KB page (R4 = page phys addr)
	MOVD	R4, R0
	MOVD	$256, R1		// 256 iterations × 16 bytes = 4KB
zero_page_t:
	STP	(ZR, ZR), (R0)
	ADD	$16, R0, R0
	SUB	$1, R1, R1
	CBNZ	R1, zero_page_t

	// DSB to ensure zeros visible
	DSB	$15

	// Heartbeat: print '.' on every page fault
	MOVD	$0x09000000, R0
	MOVW	$'.', R1
	MOVW	R1, (R0)

	// Restore registers
	// LDP x29, x30, [sp], #16
	WORD	$0xA8C17BFD
	// LDP x6, x7, [sp], #16
	WORD	$0xA8C11FE6
	// LDP x4, x5, [sp], #16
	WORD	$0xA8C117E4
	// LDP x2, x3, [sp], #16
	WORD	$0xA8C10FE2
	// LDP x0, x1, [sp], #16
	WORD	$0xA8C107E0

	// Switch back to SP_EL0
	// MSR SPSel, #0
	WORD	$0xD50040BF

	// ERET — retry faulting instruction
	ERET

// SVC handler — handle Linux syscalls from kmazarin's Go runtime.
// x8 = syscall number (not saved/clobbered by our handler).
// Stack layout: SP+0=x29/x30, SP+16=x6/x7, SP+32=x4/x5, SP+48=x2/x3, SP+64=x0/x1
// We need to set the return value (x0) in the saved area at SP+64.
// ELR_EL1 already points to the instruction after SVC.
svc_handler_t:
	// Common syscalls during Go runtime init:
	//   222 = mmap         → handle via cgo_mmap overlay (should not reach here)
	//   134 = rt_sigaction → return 0 (no signals on bare metal)
	//   135 = rt_sigprocmask → return 0
	//   132 = sigaltstack  → return 0
	//   178 = gettid       → return 1
	//   233 = madvise      → return 0
	//   160 = uname        → return -ENOSYS (caller handles error)
	//   98  = futex        → return 0
	//   261 = prlimit64    → return -ENOSYS
	//   29  = ioctl        → return -ENOSYS
	//   48  = faccessat    → return -ENOSYS
	//   56  = openat       → return -ENOSYS

	// Handle specific syscalls
	CMP	$178, R8	// gettid
	BEQ	svc_ret_1
	CMP	$186, R8	// gettid on some ABIs
	BEQ	svc_ret_1

	// For everything else, return 0 (success/no-op)
	B	svc_ret_0

svc_ret_1:
	// Return 1 in x0 (for gettid)
	MOVD	$1, R0
	MOVD	R0, 64(RSP)	// Overwrite saved x0
	B	svc_restore_t

svc_ret_0:
	// Return 0 in x0 (success/no-op)
	MOVD	ZR, 64(RSP)	// Overwrite saved x0
	B	svc_restore_t

svc_restore_t:
	// Heartbeat: print syscall number as 2 hex digits then 'v'
	MOVD	$0x09000000, R0
	// R8 still has the syscall number (we didn't clobber it)
	// Print high nibble
	LSR	$4, R8, R1
	AND	$0xF, R1, R1
	CMP	$10, R1
	BLO	svc_dig1
	ADD	$('A'-10), R1, R1
	B	svc_out1
svc_dig1:
	ADD	$'0', R1, R1
svc_out1:
	MOVW	R1, (R0)
	// Print low nibble
	AND	$0xF, R8, R1
	CMP	$10, R1
	BLO	svc_dig2
	ADD	$('A'-10), R1, R1
	B	svc_out2
svc_dig2:
	ADD	$'0', R1, R1
svc_out2:
	MOVW	R1, (R0)
	MOVW	$' ', R1
	MOVW	R1, (R0)
	// Restore registers
	WORD	$0xA8C17BFD	// LDP x29, x30, [sp], #16
	WORD	$0xA8C11FE6	// LDP x6, x7, [sp], #16
	WORD	$0xA8C117E4	// LDP x4, x5, [sp], #16
	WORD	$0xA8C10FE2	// LDP x2, x3, [sp], #16
	WORD	$0xA8C107E0	// LDP x0, x1, [sp], #16
	// Switch back to SP_EL0
	WORD	$0xD50040BF	// MSR SPSel, #0
	ERET

halt_t:
	// Unhandled exception — dump FAR and ESR to UART, then halt
	MOVD	$0x09000000, R5
	MOVW	$'X', R6
	MOVW	R6, (R5)
	// Print FAR_EL1 as hex
	MRS	FAR_EL1, R0
	MOVW	$'F', R6
	MOVW	R6, (R5)
	MOVW	$':', R6
	MOVW	R6, (R5)
	// Print 16 hex digits of R0
	MOVD	$60, R3		// shift = 60 (start from MSB)
hex_loop_t:
	LSR	R3, R0, R1
	AND	$0xF, R1, R1
	CMP	$10, R1
	BLO	digit_t
	ADD	$('A'-10), R1, R1
	B	emit_t
digit_t:
	ADD	$'0', R1, R1
emit_t:
	MOVW	R1, (R5)
	SUB	$4, R3, R3
	CMP	$0, R3
	BGE	hex_loop_t
	// Print ESR_EL1
	MOVW	$' ', R6
	MOVW	R6, (R5)
	MOVW	$'E', R6
	MOVW	R6, (R5)
	MOVW	$':', R6
	MOVW	R6, (R5)
	MRS	ESR_EL1, R0
	MOVD	$28, R3		// ESR is 32-bit, print 8 hex digits
hex_loop_e:
	LSR	R3, R0, R1
	AND	$0xF, R1, R1
	CMP	$10, R1
	BLO	digit_e
	ADD	$('A'-10), R1, R1
	B	emit_e
digit_e:
	ADD	$'0', R1, R1
emit_e:
	MOVW	R1, (R5)
	SUB	$4, R3, R3
	CMP	$0, R3
	BGE	hex_loop_e
	// Print ELR_EL1
	MOVW	$' ', R6
	MOVW	R6, (R5)
	MOVW	$'P', R6
	MOVW	R6, (R5)
	MOVW	$':', R6
	MOVW	R6, (R5)
	MRS	ELR_EL1, R0
	MOVD	$60, R3
hex_loop_p:
	LSR	R3, R0, R1
	AND	$0xF, R1, R1
	CMP	$10, R1
	BLO	digit_p
	ADD	$('A'-10), R1, R1
	B	emit_p
digit_p:
	ADD	$'0', R1, R1
emit_p:
	MOVW	R1, (R5)
	SUB	$4, R3, R3
	CMP	$0, R3
	BGE	hex_loop_p
	MOVW	$'\n', R6
	MOVW	R6, (R5)
	// Halt
halt_spin_t:
	WFI
	B	halt_spin_t

// diplomatSyncEL1h handles synchronous exceptions from EL1h (SP_EL1 mode).
// Same handler, but we're already on SP_EL1 so no SPSel switch needed.
TEXT ·diplomatSyncEL1h(SB), NOSPLIT|NOFRAME, $0
	// Already on SP_EL1, save registers
	WORD	$0xA9BF07E0	// STP x0, x1, [sp, #-16]!
	WORD	$0xA9BF0FE2	// STP x2, x3, [sp, #-16]!
	WORD	$0xA9BF17E4	// STP x4, x5, [sp, #-16]!
	WORD	$0xA9BF1FE6	// STP x6, x7, [sp, #-16]!
	WORD	$0xA9BF7BFD	// STP x29, x30, [sp, #-16]!

	MRS	ESR_EL1, R0
	LSR	$26, R0, R1
	CMP	$0x25, R1
	BNE	halt_h

	MRS	FAR_EL1, R2
	AND	$~0xFFF, R2, R2

	MOVD	$0xFFFF000100000000, R3
	CMP	R3, R2
	BLO	halt_h

	MOVD	$0xFFFF100000000000, R3
	CMP	R3, R2
	BHS	halt_h

	// Allocate page from pool
	MOVD	$main·demandPagePool(SB), R3
	MOVD	(R3), R4
	MOVD	8(R3), R5
	CMP	R5, R4
	BHS	halt_h
	ADD	$4096, R4, R6
	MOVD	R6, (R3)

	// Walk page tables (same logic as EL1t handler)
	MRS	TTBR1_EL1, R0
	AND	$~0xFFF, R0, R0

	LSR	$39, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1
	MOVD	(R1), R0
	TST	$1, R0
	BEQ	halt_h	// L0 not valid — fatal for EL1h
	AND	$~0xFFF, R0, R0

	LSR	$30, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1
	MOVD	(R1), R0
	TST	$1, R0
	BEQ	halt_h
	AND	$~0xFFF, R0, R0

	LSR	$21, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1
	MOVD	(R1), R0
	TST	$1, R0
	BEQ	halt_h
	TST	$2, R0
	BEQ	halt_h
	AND	$~0xFFF, R0, R0

	LSR	$12, R2, R1
	AND	$0x1FF, R1, R1
	LSL	$3, R1, R1
	ADD	R0, R1, R1

	MOVD	$0x70F, R5
	ORR	R5, R4, R0
	MOVD	R0, (R1)

	DSB	$15
	LSR	$12, R2, R0
	WORD	$0xD5088720	// TLBI VAE1, X0
	DSB	$15
	ISB	$15

	// Zero page
	MOVD	R4, R0
	MOVD	$256, R1
zero_page_h:
	STP	(ZR, ZR), (R0)
	ADD	$16, R0, R0
	SUB	$1, R1, R1
	CBNZ	R1, zero_page_h
	DSB	$15

	// Restore and return
	WORD	$0xA8C17BFD	// LDP x29, x30, [sp], #16
	WORD	$0xA8C11FE6	// LDP x6, x7, [sp], #16
	WORD	$0xA8C117E4	// LDP x4, x5, [sp], #16
	WORD	$0xA8C10FE2	// LDP x2, x3, [sp], #16
	WORD	$0xA8C107E0	// LDP x0, x1, [sp], #16
	// No SPSel switch needed — already on SP_EL1
	ERET

halt_h:
	// Unhandled exception — write 'Y' to UART and halt
	MOVD	$0x09000000, R0
	MOVW	$'Y', R1
	MOVW	R1, (R0)
	WFI
	B	halt_h
