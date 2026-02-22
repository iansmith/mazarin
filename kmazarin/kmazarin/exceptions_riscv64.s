//go:build !test_stubs

// exceptions_riscv64.s - RISC-V trap handler
#include "go_abi_macros_riscv64.h"
//
// Single trap entry point set via stvec CSR.
// Handles: ecall (syscall), page faults, timer interrupts, external interrupts.
//
// Trap frame layout (saved on exception stack via sscratch swap):
//   Frame[0]=x1(ra) at SP+0
//   Frame[1]=x2(sp) at SP+8  (original sp before trap)
//   Frame[2]=x3(gp) at SP+16
//   ...
//   Frame[30]=x31(t6) at SP+240
//   Frame[31]=sepc at SP+248
//   Frame[32]=sstatus at SP+256
// Total: 33 * 8 = 264 bytes

#include "textflag.h"

#define FRAME_SIZE 264

// ============================================================================
// Trap entry - called from stvec
// ============================================================================
// On entry:
//   - sscratch contains the exception stack pointer
//   - All registers contain pre-trap values
//   - scause, stval, sepc are set by hardware
//
TEXT ·trapEntry(SB), NOSPLIT|NOFRAME, $0
	// Swap sp and sscratch: sp now points to exception stack
	// CSRRW sp, sscratch, sp
	WORD	$0x14011173

	// Allocate trap frame
	ADD	$-FRAME_SIZE, X2

	// Save all GPRs (x1-x31) into frame
	// x1 (ra)
	MOV	X1, 0(X2)
	// x2 (original sp) is in sscratch - save later
	MOV	X3, 16(X2)		// gp
	MOV	TP, 24(X2)		// tp
	MOV	X5, 32(X2)		// t0
	MOV	X6, 40(X2)		// t1
	MOV	X7, 48(X2)		// t2
	MOV	X8, 56(X2)		// s0
	MOV	X9, 64(X2)		// s1
	MOV	X10, 72(X2)		// a0
	MOV	X11, 80(X2)		// a1
	MOV	X12, 88(X2)		// a2
	MOV	X13, 96(X2)		// a3
	MOV	X14, 104(X2)		// a4
	MOV	X15, 112(X2)		// a5
	MOV	X16, 120(X2)		// a6
	MOV	X17, 128(X2)		// a7
	MOV	X18, 136(X2)		// s2
	MOV	X19, 144(X2)		// s3
	MOV	X20, 152(X2)		// s4
	MOV	X21, 160(X2)		// s5
	MOV	X22, 168(X2)		// s6
	MOV	X23, 176(X2)		// s7
	MOV	X24, 184(X2)		// s8
	MOV	X25, 192(X2)		// s9
	MOV	X26, 200(X2)		// s10
	MOV	g, 208(X2)		// s11 (g)
	MOV	X28, 216(X2)		// t3
	MOV	X29, 224(X2)		// t4
	MOV	X30, 232(X2)		// t5
	MOV	X31, 240(X2)		// t6

	// Read original sp from sscratch and save to frame
	// CSRR t0, sscratch
	WORD	$0x140022F3		// csrr t0(x5), sscratch
	MOV	X5, 8(X2)		// save original sp

	// Save sepc
	// CSRR t0, sepc
	WORD	$0x141022F3		// csrr t0, sepc
	MOV	X5, 248(X2)		// save sepc

	// Save sstatus
	// CSRR t0, sstatus
	WORD	$0x100022F3		// csrr t0, sstatus
	MOV	X5, 256(X2)		// save sstatus

	// Store exception stack pointer to sscratch for nested traps.
	// CRITICAL: We must reserve space between the outer frame and where
	// nested trap frames start. GO_CALL macros run Go code on the exception
	// stack BELOW the outer frame (SyscallDispatch → SyscallWaitSoftIRQ → etc.).
	// If a nested page fault occurs during Go execution, the nested trap reads
	// sscratch and allocates its 264-byte frame there. Without reservation,
	// the nested frame overlaps with the active Go stack frames, corrupting
	// return addresses and causing instruction page faults at garbage PCs.
	//
	// Reserve 4KB (0x1000) for Go stack usage. The exception stack is 16KB,
	// so this leaves plenty of room for nested trap frames below.
	MOV	X2, T0
	ADD	$-2048, T0
	ADD	$-2048, T0		// T0 = SP - 4096 (reserve 4KB for Go stack)
	// CSRW sscratch, T0(x5)
	WORD	$0x14029073		// csrw sscratch, t0

	// CRITICAL: Switch to kmazarin's g before calling any Go code!
	// X27 (g) currently contains userspace's g, but kernel Go functions
	// need kmazarin's g for stack growth checks and runtime access.
	// The user's g is already saved in the trap frame at offset 208.
	MOV	·kmazarinG0Addr(SB), T1
	BEQ	T1, ZERO, skip_g_switch
	MOV	T1, g			// Set g = kmazarin's g0
skip_g_switch:

	// Read scause to determine trap type
	// CSRR t0, scause
	WORD	$0x142022F3		// csrr t0, scause

	// Check interrupt bit (bit 63)
	MOV	$-1, T1
	// Shift right by 63 to get the interrupt bit
	SRL	$63, T0, T1		// T1 = interrupt bit
	BNE	T1, ZERO, handle_interrupt

	// === Exception dispatch ===
	// t0 = scause (exception code, no interrupt bit)

	// Breakpoint = 3 (used as synthetic syscall on RISC-V, since ECALL
	// from S-mode goes to M-mode/OpenSBI, not to our trap handler)
	MOV	$3, T1
	BEQ	T0, T1, handle_ecall

	// ecall from S-mode = 9, ecall from U-mode = 8
	MOV	$9, T1
	BEQ	T0, T1, handle_ecall
	MOV	$8, T1
	BEQ	T0, T1, handle_ecall

	// Access faults: 5=load, 7=store
	// These occur when PTE walk succeeds but physical access is denied.
	// Route through page fault handler for diagnostics.
	MOV	$5, T1
	BEQ	T0, T1, handle_page_fault
	MOV	$7, T1
	BEQ	T0, T1, handle_page_fault

	// Page faults: 12=instruction, 13=load, 15=store
	MOV	$12, T1
	BEQ	T0, T1, handle_page_fault
	MOV	$13, T1
	BEQ	T0, T1, handle_page_fault
	MOV	$15, T1
	BEQ	T0, T1, handle_page_fault

	// Unknown exception - print: U<scause>@<sepc>[<stval>
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x55, T4		// 'U'
	MOVB	T4, (T3)
	// Print scause digit
	ADD	$0x30, T0, T4		// '0' + scause
	MOVB	T4, (T3)

	// Print '@' then sepc (faulting PC) as 16 hex digits
	// NOTE: T3=X28, so 'MOV $10, X28' clobbers UART addr. Reload after.
	MOV	$0x40, T4		// '@'
	MOVB	T4, (T3)
	MOV	248(X2), T1		// sepc from frame
	MOV	$60, T4			// start bit position
unknown_sepc_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, unknown_sepc_digit
	ADD	$0x37, T5, T5
	JMP	unknown_sepc_print
unknown_sepc_digit:
	ADD	$0x30, T5, T5
unknown_sepc_print:
	MOV	$0xFFFFFFFF10000000, T3	// reload UART (X28 was clobbered)
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, unknown_sepc_loop

	// Print '[' then stval as 16 hex digits then ']'
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x5B, T4		// '['
	MOVB	T4, (T3)
	// CSRR T1, stval
	WORD	$0x14302373		// csrr t1(x6), stval
	MOV	$60, T4
unknown_stval_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, unknown_stval_digit
	ADD	$0x37, T5, T5
	JMP	unknown_stval_print
unknown_stval_digit:
	ADD	$0x30, T5, T5
unknown_stval_print:
	MOV	$0xFFFFFFFF10000000, T3	// reload UART (X28 was clobbered)
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, unknown_stval_loop
	MOV	$0x5D, T4		// ']'
	MOVB	T4, (T3)

	// Also advance sepc past the faulting instruction (4 bytes)
	// to avoid infinite re-trap on same illegal instruction
	MOV	248(X2), T0
	ADD	$4, T0, T0
	MOV	T0, 248(X2)

	JMP	trap_return

handle_ecall:
ecall_dispatch:

	// ---- DIAGNOSTIC: count syscalls ----
	MOV	$·syscallDiagCount(SB), T0
	MOV	(T0), T1
	ADD	$1, T1
	MOV	T1, (T0)
	// Print 'E' every 256th syscall
	AND	$255, T1, T2
	BNE	T2, ZERO, ecall_skip_marker
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$0x45, T1		// 'E'
	MOVB	T1, (T0)
ecall_skip_marker:
	// ---- END DIAGNOSTIC ----

	// Syscall dispatch
	// Advance sepc past ECALL instruction (4 bytes)
	MOV	248(X2), T0		// sepc
	ADD	$4, T0, T0
	MOV	T0, 248(X2)		// update saved sepc
	// Also update the CSR
	// CSRW sepc, t0
	WORD	$0x14129073		// csrw sepc, t0(x5)

	// Save ELR (sepc) for clone — T0 has sepc from above
	GO_CALL_1_0(·SetSyscallELR, T0)

	// Save SPSR (sstatus) for clone
	MOV	256(X2), S2		// sstatus from frame (callee-saved scratch)
	GO_CALL_1_0(·SetSyscallSPSR, S2)

	// Dispatch syscall: SyscallDispatch(num, a0, a1, a2, a3, a4, a5)
	// RISC-V syscall convention: a7=number, a0-a5=args
	// Load all 7 args from trap frame before macro adjusts SP
	MOV	128(X2), T0		// a7 from frame = syscall number
	MOV	72(X2), T1		// a0 from frame
	MOV	80(X2), T2		// a1
	MOV	88(X2), A3		// a2
	MOV	96(X2), A4		// a3
	MOV	104(X2), A5		// a4
	MOV	112(X2), A6		// a5
	GO_CALL_7_1(·SyscallDispatch, T0, T1, T2, A3, A4, A5, A6)
	// T0 = return value
	MOV	T0, 72(X2)		// Store return value in saved a0 slot

	// Check context switch
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	// T0 = target or -1
	MOV	$0, T1
	BLE	T0, T1, ecall_no_switch

	MOV	T0, S2			// save target in callee-saved S2

	// Context switch: DoContextSwitch(framePtr, targetPtr) uintptr
	MOV	X2, T1			// frame pointer
	GO_CALL_2_1(·DoContextSwitch, T1, S2)
	MOV	T0, S2			// new context pointer

	BEQ	S2, ZERO, trap_return
	JMP	load_context_and_sret

ecall_no_switch:
	JMP	trap_return

handle_page_fault:
	// Read scause to check for instruction page fault
	// CSRR a3, scause
	WORD	$0x142029F3		// csrr s3(x19), scause

	// Read stval (fault address)
	// CSRR t1, stval
	WORD	$0x14302373		// csrr t1(x6), stval

	// Check: instruction page fault (scause=12) at heap address → reject
	// Heap pages are mapped without X bit, so instruction faults loop forever
	MOV	$12, T2
	BNE	X19, T2, pf_call_handler	// not instruction fault → handle normally

	// Save stval in X20 (S4) for later use by Go diagnostic function
	MOV	T1, X20				// X20 = stval (preserved across prints)

	// Instruction page fault: print 'X' marker and diagnostic, then halt
	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x58, X29			// 'X'
	MOVB	X29, (X28)

	// Print stval (fault address) as 16 hex digits: "X[<stval>]"
	MOV	$0x5B, X29			// '['
	MOVB	X29, (X28)
	MOV	T1, X19				// save stval in X19
	MOV	$60, T4
pf_ifault_addr_loop:
	SRL	T4, X19, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_ifault_digit
	ADD	$0x37, T5, T5
	JMP	pf_ifault_print
pf_ifault_digit:
	ADD	$0x30, T5, T5
pf_ifault_print:
	MOV	$0xFFFFFFFF10000000, X28
	MOVB	T5, (X28)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_ifault_addr_loop

	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x5D, X29			// ']'
	MOVB	X29, (X28)

	// Print sepc for context
	MOV	$0x40, X29			// '@'
	MOVB	X29, (X28)
	MOV	248(X2), X19			// sepc from trap frame
	MOV	$60, T4
pf_ifault_sepc_loop:
	SRL	T4, X19, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_ifault_sepc_digit
	ADD	$0x37, T5, T5
	JMP	pf_ifault_sepc_print
pf_ifault_sepc_digit:
	ADD	$0x30, T5, T5
pf_ifault_sepc_print:
	MOV	$0xFFFFFFFF10000000, X28
	MOVB	T5, (X28)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_ifault_sepc_loop

	// Print RA from frame
	MOV	$0xFFFFFFFF10000000, X28	// reload UART (was clobbered by hex loop)
	MOV	$0x3E, X29			// '>'
	MOVB	X29, (X28)
	MOV	0(X2), X19			// RA from trap frame
	MOV	$60, T4
pf_ifault_ra_loop:
	SRL	T4, X19, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_ifault_ra_digit
	ADD	$0x37, T5, T5
	JMP	pf_ifault_ra_print
pf_ifault_ra_digit:
	ADD	$0x30, T5, T5
pf_ifault_ra_print:
	MOV	$0xFFFFFFFF10000000, X28
	MOVB	T5, (X28)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_ifault_ra_loop

	// Call DumpInstructionPageFaultAsm to walk and print PTE chain
	GO_CALL_1_0(·DumpInstructionPageFaultAsm, X20)

	// Halt — do NOT try to handle instruction fetch at non-executable page
pf_ifault_halt:
	WORD	$0x10500073			// wfi
	JMP	pf_ifault_halt

pf_call_handler:
	// Call HandlePageFaultAsm(faultAddr)
	// T1 has stval from CSR read above
	MOV	T1, S2			// save faultAddr in callee-saved S2
	GO_CALL_1_1(·HandlePageFaultAsm, S2)
	// T0 = handled?

	BEQ	T0, ZERO, pf_not_handled
	JMP	trap_return

pf_not_handled:
	// Kernel handler returned 0 — try user page fault handler,
	// but ONLY if the trap came from U-mode (SPP=0 in saved sstatus).
	// If the trap came from S-mode (kernel code), skip user handler.
	MOV	256(X2), T3		// saved sstatus from trap frame
	AND	$0x100, T3, T3		// SPP bit (bit 8)
	BNE	T3, ZERO, pf_really_not_handled	// SPP=1 → S-mode fault, skip

	// U-mode fault: S2 still has faultAddr (callee-saved, preserved across GO_CALL)
	GO_CALL_1_1(·HandleUserPageFaultAsm, S2)
	BNE	T0, ZERO, pf_user_handled

	JMP	pf_really_not_handled

pf_user_handled:
	// SFENCE.VMA for the faulting page before returning to user mode.
	// S2 has the faulting VA — flush its TLB entry to ensure the
	// newly-mapped page is visible after SRET.
	// SFENCE.VMA S2(x18), zero
	WORD	$0x12090073

	JMP	trap_return

pf_really_not_handled:

	// Page fault NOT handled by kernel or user handler.
	// Print diagnostic: "F@<sepc>/<scause>[<stval>" then halt.
	// This catches unmapped low-address accesses (identity map removed).
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x46, T5			// 'F'
	MOVB	T5, (T3)
	MOV	$0x40, T5			// '@'
	MOVB	T5, (T3)

	// Print sepc (faulting instruction PC) as 16 hex digits
	MOV	248(X2), T1			// sepc from trap frame
	MOV	$60, T4
pf_sepc_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_sepc_digit
	ADD	$0x37, T5, T5
	JMP	pf_sepc_out
pf_sepc_digit:
	ADD	$0x30, T5, T5
pf_sepc_out:
	MOV	$0xFFFFFFFF10000000, T3		// reload UART (X28 clobbered)
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_sepc_loop

	// Print '/' then scause digit
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x2F, T5			// '/'
	MOVB	T5, (T3)
	WORD	$0x142022F3			// csrr t0(x5), scause
	AND	$0xF, T0, T5
	ADD	$0x30, T5, T5
	MOVB	T5, (T3)

	// Print '[' then stval as 16 hex digits
	MOV	$0x5B, T5			// '['
	MOVB	T5, (T3)
	WORD	$0x14302373			// csrr t1(x6), stval
	MOV	$60, T4
pf_stval_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_stval_digit
	ADD	$0x37, T5, T5
	JMP	pf_stval_out
pf_stval_digit:
	ADD	$0x30, T5, T5
pf_stval_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_stval_loop

	// Print RA (return address) from trap frame: ">RA<"
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x3E, T5			// '>'
	MOVB	T5, (T3)
	MOV	0(X2), T1			// X1 (RA) from trap frame offset 0
	MOV	$60, T4
pf_ra_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_ra_digit
	ADD	$0x37, T5, T5
	JMP	pf_ra_out
pf_ra_digit:
	ADD	$0x30, T5, T5
pf_ra_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_ra_loop
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x3C, T5			// '<'
	MOVB	T5, (T3)

	// Also print saved SP from trap frame: "SP=xxxx"
	MOV	$0x53, T5			// 'S'
	MOVB	T5, (T3)
	MOV	$0x50, T5			// 'P'
	MOVB	T5, (T3)
	MOV	$0x3D, T5			// '='
	MOVB	T5, (T3)
	MOV	8(X2), T1			// X2 (original SP) from trap frame offset 8
	MOV	$60, T4
pf_sp_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_sp_digit
	ADD	$0x37, T5, T5
	JMP	pf_sp_out
pf_sp_digit:
	ADD	$0x30, T5, T5
pf_sp_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_sp_loop

	// Print A1 (X11 = argv in sysargs): "A1=<16hex>"
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x41, T5			// 'A'
	MOVB	T5, (T3)
	MOV	$0x31, T5			// '1'
	MOVB	T5, (T3)
	MOV	$0x3D, T5			// '='
	MOVB	T5, (T3)
	MOV	80(X2), T1			// X11 (A1) from trap frame
	MOV	$60, T4
pf_a1_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_a1_digit
	ADD	$0x37, T5, T5
	JMP	pf_a1_out
pf_a1_digit:
	ADD	$0x30, T5, T5
pf_a1_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_a1_loop

	// Print T0 (X5 = n in sysargs loop): "N=<16hex>"
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x4E, T5			// 'N'
	MOVB	T5, (T3)
	MOV	$0x3D, T5			// '='
	MOVB	T5, (T3)
	MOV	32(X2), T1			// X5 (T0) from trap frame
	MOV	$60, T4
pf_n_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_n_digit
	ADD	$0x37, T5, T5
	JMP	pf_n_out
pf_n_digit:
	ADD	$0x30, T5, T5
pf_n_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_n_loop

	// Print G (X27 = goroutine pointer): "G=<16hex>"
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x47, T5			// 'G'
	MOVB	T5, (T3)
	MOV	$0x3D, T5			// '='
	MOVB	T5, (T3)
	MOV	208(X2), T1			// X27 (g register) from trap frame
	MOV	$60, T4
pf_g_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_g_digit
	ADD	$0x37, T5, T5
	JMP	pf_g_out
pf_g_digit:
	ADD	$0x30, T5, T5
pf_g_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_g_loop

	// Print return address chain from goroutine stack
	// ONLY for kernel faults (SPP=1) — user stack is inaccessible (SUM=0).
	MOV	256(X2), T5			// saved sstatus from trap frame
	AND	$0x100, T5, T5			// SPP bit (bit 8)
	BEQ	T5, ZERO, pf_skip_stack_walk	// SPP=0 → user fault, skip

	// *(SP+0) = sysargs stored RA, *(SP+72) = args stored RA
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x43, T5			// 'C'
	MOVB	T5, (T3)
	MOV	$0x3D, T5			// '='
	MOVB	T5, (T3)
	MOV	8(X2), T1			// original SP from trap frame
	ADD	$72, T1				// SP + 72 = args frame
	MOV	(T1), T1			// *(SP+72) = args stored RA
	MOV	$60, T4
pf_caller_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_caller_digit
	ADD	$0x37, T5, T5
	JMP	pf_caller_out
pf_caller_digit:
	ADD	$0x30, T5, T5
pf_caller_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_caller_loop

	// Print *(SP+96) = args.abi0 stored RA (who called args.abi0?)
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x2F, T5			// '/'
	MOVB	T5, (T3)
	MOV	8(X2), T1			// original SP from trap frame
	ADD	$96, T1				// SP + 72 + 24 = args.abi0 frame
	MOV	(T1), T1			// *(SP+96) = args.abi0 stored RA
	MOV	$60, T4
pf_caller2_loop:
	SRL	T4, T1, T5
	AND	$0xF, T5
	MOV	$10, X28
	BLT	T5, X28, pf_caller2_digit
	ADD	$0x37, T5, T5
	JMP	pf_caller2_out
pf_caller2_digit:
	ADD	$0x30, T5, T5
pf_caller2_out:
	MOV	$0xFFFFFFFF10000000, T3
	MOVB	T5, (T3)
	ADD	$-4, T4
	MOV	$-4, X28
	BNE	T4, X28, pf_caller2_loop

pf_skip_stack_walk:

	// Check if fault came from user mode (SPP bit in saved sstatus)
	MOV	256(X2), T3			// saved sstatus from trap frame
	AND	$0x100, T3, T3			// SPP bit (bit 8)
	BNE	T3, ZERO, pf_unhandled_halt	// Kernel fault → still halt

	// User fault: kill the faulting thread instead of halting the system.
	// ThreadExitAsm marks thread exited, finds next ready thread,
	// returns pointer to its ThreadContext (or 0 if no threads left).
	GO_CALL_0_1(·ThreadExitAsm)
	BEQ	T0, ZERO, pf_unhandled_halt	// No threads left → halt
	MOV	T0, S2				// S2 = ThreadContext pointer
	JMP	load_context_and_sret

	// Halt - kernel fault or no threads remain
pf_unhandled_halt:
	WORD	$0x10500073			// wfi
	JMP	pf_unhandled_halt

handle_interrupt:
	// Mask off interrupt bit (bit 63) to get cause code
	// t0 still has full scause. Use SLL+SRL to clear bit 63.
	SLL	$1, T0, T0
	SRL	$1, T0, T0		// T0 = cause without interrupt bit

	// Software interrupt = cause 1 (S-mode software)
	MOV	$1, T1
	BEQ	T0, T1, handle_software_interrupt

	// Timer interrupt = cause 5 (S-mode timer)
	MOV	$5, T1
	BEQ	T0, T1, handle_timer_interrupt

	// External interrupt = cause 9 (S-mode external)
	MOV	$9, T1
	BEQ	T0, T1, handle_external_interrupt

	// Unknown interrupt - print cause code and disable that interrupt
	MOV	$0xFFFFFFFF10000000, T3
	MOV	$0x49, T4		// 'I'
	MOVB	T4, (T3)
	// Print cause as single hex digit
	AND	$0xF, T0, T4
	MOV	$10, T1
	BLT	T4, T1, unk_irq_digit
	ADD	$0x37, T4, T4
	JMP	unk_irq_print
unk_irq_digit:
	ADD	$0x30, T4, T4
unk_irq_print:
	MOVB	T4, (T3)
	// Disable ALL S-mode interrupt enables to stop the loop
	// CSRW sie, zero
	WORD	$0x10401073		// csrw sie, zero
	JMP	trap_return

handle_timer_interrupt:
	// S-mode timer interrupt (cause 5)

	// ---- DIAGNOSTIC: Increment timer IRQ counter and print marker ----
	// This runs BEFORE any Go code to verify the timer hardware is firing.
	MOV	$·timerDiagCount(SB), T0
	MOV	(T0), T1
	ADD	$1, T1
	MOV	T1, (T0)
	// Print 'T' every 64th timer interrupt (~6.4s at 100ms interval)
	AND	$63, T1, T2
	BNE	T2, ZERO, timer_skip_marker
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$0x54, T1		// 'T'
	MOVB	T1, (T0)
timer_skip_marker:
	// ---- END DIAGNOSTIC ----

	// Call pure assembly preemption handler (g poisoning, deadline tracking).
	// X2 = trap frame base. Handler saves/restores A0-A5.
	CALL	mazzy∕kmazarin∕kirq·TimerIRQHandlerAsm(SB)

	// Dispatch timer IRQ (Go handler: SATP verify, deadline processing, re-arm)
	// Load all args from trap frame before macro adjusts SP
	MOV	$5, T0			// IRQ number
	MOV	X2, T1			// frame pointer
	MOV	248(X2), T2		// sepc (ELR equivalent)
	MOV	8(X2), A3		// original sp
	GO_CALL_4_0(·TimerIRQHandler, T0, T1, T2, A3)

	// ---- DIAGNOSTIC: TimerIRQHandler completed (rearm happened) ----
	MOV	$·timerHandlerDoneCount(SB), T0
	MOV	(T0), T1
	ADD	$1, T1
	MOV	T1, (T0)
	// Print 'R' (rearm done) every 64th time
	AND	$63, T1, T2
	BNE	T2, ZERO, timer_rearm_skip
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$0x52, T1		// 'R'
	MOVB	T1, (T0)
timer_rearm_skip:
	// ---- END DIAGNOSTIC ----

	// Process deadline queue in top-half context.
	// CRITICAL: Must run ProcessDeadlines HERE (not in bottom half) because
	// bottom half goroutines run on kernel threads that may be blocked on futex.
	// If all kernel threads are blocked, the bottom half never runs, and timed
	// futex waits / nanosleep deadlines never fire — starving the kernel.
	// (Same pattern as ARM64 exceptions_arm64.s:969-987)
	GO_CALL_0_0(·ProcessDeadlinesTopHalf)

	// Check thread preemption
	MOV	X2, S2			// frame pointer (callee-saved)
	GO_CALL_1_1(·CheckThreadPreemption, S2)
	MOV	T0, S2			// new context or 0

	BEQ	S2, ZERO, timer_no_switch

	// ---- DIAGNOSTIC: count context switches ----
	MOV	$·timerCtxSwitchCount(SB), T0
	MOV	(T0), T1
	ADD	$1, T1
	MOV	T1, (T0)
	// Print 'S' every 64th context switch
	AND	$63, T1, T2
	BNE	T2, ZERO, timer_switch_skip_marker
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$0x53, T1		// 'S'
	MOVB	T1, (T0)
timer_switch_skip_marker:
	// ---- END DIAGNOSTIC ----

	JMP	load_context_and_sret

timer_no_switch:
	// ---- DIAGNOSTIC: count no-switch timer returns ----
	MOV	$·timerNoSwitchCount(SB), T0
	MOV	(T0), T1
	ADD	$1, T1
	MOV	T1, (T0)
	// Print 'N' every 64th no-switch return
	AND	$63, T1, T2
	BNE	T2, ZERO, timer_no_switch_skip
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$0x4E, T1		// 'N'
	MOVB	T1, (T0)
timer_no_switch_skip:
	// ---- END DIAGNOSTIC ----

	JMP	trap_return

handle_software_interrupt:
	// S-mode software interrupt (cause 1)
	// Clear SSIP bit (bit 1) in SIP register to acknowledge
	MOV	$2, T0			// bit 1 = SSIP
	// CSRC sip, t0  = csrrc x0, sip, t0(x5)
	WORD	$0x1442B073		// csrrc x0, sip, x5
	JMP	trap_return

handle_external_interrupt:
	// S-mode external interrupt (cause 9) — PLIC external IRQ
	// Dispatch via PLIC: claim interrupt, call handler, complete.
	GO_CALL_0_0(·PLICDispatchIRQ)
	JMP	trap_return

// ============================================================================
// Trap return - restore GPRs and SRET
// ============================================================================
trap_return:
	// Restore sepc
	MOV	248(X2), T0
	// CSRW sepc, t0
	WORD	$0x14129073		// csrw sepc, t0(x5)

	// Restore sstatus
	MOV	256(X2), T0
	// CSRW sstatus, t0
	WORD	$0x10029073		// csrw sstatus, t0(x5)

	// Restore all GPRs (except sp, which we restore via sscratch)
	MOV	0(X2), X1		// ra
	// Skip x2 (sp) - restore last via sscratch
	MOV	16(X2), X3
	MOV	24(X2), TP
	MOV	32(X2), X5
	MOV	40(X2), X6
	MOV	48(X2), X7
	MOV	56(X2), X8
	MOV	64(X2), X9
	MOV	72(X2), X10
	MOV	80(X2), X11
	MOV	88(X2), X12
	MOV	96(X2), X13
	MOV	104(X2), X14
	MOV	112(X2), X15
	MOV	120(X2), X16
	MOV	128(X2), X17
	MOV	136(X2), X18
	MOV	144(X2), X19
	MOV	152(X2), X20
	MOV	160(X2), X21
	MOV	168(X2), X22
	MOV	176(X2), X23
	MOV	184(X2), X24
	MOV	192(X2), X25
	MOV	200(X2), X26
	MOV	208(X2), g		// g
	MOV	216(X2), X28
	MOV	224(X2), X29
	MOV	232(X2), X30
	MOV	240(X2), X31

	// Restore original sp properly:
	// 1. Put original sp (from frame[1]) into sscratch
	MOV	8(X2), X5		// T0 = original sp from frame
	// CSRW sscratch, T0(x5)
	WORD	$0x14029073		// sscratch = original sp

	// 2. Re-restore T0 to pre-trap value (clobbered in step 1)
	MOV	32(X2), X5		// T0 = pre-trap X5

	// 3. Deallocate frame (X2 = exception stack top)
	ADD	$FRAME_SIZE, X2

	// 4. Swap: sp ← sscratch (= original sp), sscratch ← X2 (= exc stack top)
	// CSRRW sp, sscratch, sp
	WORD	$0x14011173

	// SRET
	WORD	$0x10200073

// ============================================================================
// Load new thread context and SRET
// ============================================================================
// S2 (x18) = pointer to ThreadContext
load_context_and_sret:
	// Load SEPC
	MOV	256(S2), A0
	// CSRW sepc, a0
	WORD	$0x14151073

	// Load SSTATUS
	MOV	264(S2), A0
	// CSRW sstatus, a0
	WORD	$0x10051073

	// TLB flush
	WORD	$0x12000073		// sfence.vma

	// Load GPRs from new context
	MOV	8(S2), X1		// ra
	MOV	24(S2), X3
	MOV	32(X18), TP
	MOV	40(S2), X5
	MOV	48(S2), X6
	MOV	56(S2), X7
	MOV	64(S2), X8
	MOV	72(S2), X9
	MOV	80(S2), X10
	MOV	88(S2), X11
	MOV	96(S2), X12
	MOV	104(S2), X13
	MOV	112(S2), X14
	MOV	120(S2), X15
	MOV	128(S2), X16
	MOV	136(S2), X17
	MOV	152(S2), X19
	MOV	160(S2), X20
	MOV	168(S2), X21
	MOV	176(S2), X22
	MOV	184(S2), X23
	MOV	192(S2), X24
	MOV	200(S2), X25
	MOV	208(S2), X26
	MOV	216(S2), g		// g = X27
	MOV	224(S2), X28
	MOV	232(S2), X29
	MOV	240(S2), X30
	MOV	248(S2), X31

	// CRITICAL: Deallocate the trap frame on the exception stack BEFORE
	// saving to sscratch. Without this, each context switch via this path
	// leaks FRAME_SIZE bytes on the exception stack, causing overflow after
	// ~25 timer preemptions (264 bytes × 25 = 6600 bytes).
	ADD	$FRAME_SIZE, X2

	// Restore sp via sscratch swap
	MOV	16(S2), A0		// target sp
	WORD	$0x14051073		// csrw sscratch, a0
	MOV	80(S2), X10		// reload a0 (X[10] at offset 80)
	MOV	144(S2), X18		// load s2 (X[18] at offset 144)
	WORD	$0x14011173		// csrrw sp, sscratch, sp

	// SRET
	WORD	$0x10200073
