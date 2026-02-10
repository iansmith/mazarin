//go:build !test_stubs

// exceptions_riscv64.s - RISC-V trap handler
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

	// Store exception stack back to sscratch for nested traps
	// CSRW sscratch, sp
	WORD	$0x14011073		// csrw sscratch, sp(x2)

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

	// ecall from S-mode = 9, ecall from U-mode = 8
	MOV	$9, T1
	BEQ	T0, T1, handle_ecall
	MOV	$8, T1
	BEQ	T0, T1, handle_ecall

	// Page faults: 12=instruction, 13=load, 15=store
	MOV	$12, T1
	BEQ	T0, T1, handle_page_fault
	MOV	$13, T1
	BEQ	T0, T1, handle_page_fault
	MOV	$15, T1
	BEQ	T0, T1, handle_page_fault

	// Unknown exception - return
	JMP	trap_return

handle_ecall:
	// Syscall dispatch
	// Advance sepc past ECALL instruction (4 bytes)
	MOV	248(X2), T0		// sepc
	ADD	$4, T0, T0
	MOV	T0, 248(X2)		// update saved sepc
	// Also update the CSR
	// CSRW sepc, t0
	WORD	$0x14129073		// csrw sepc, t0(x5)

	// Save ELR (sepc) for clone
	ADD	$-16, X2
	MOV	T0, 8(X2)
	CALL	·SetSyscallELR(SB)
	ADD	$16, X2

	// Save SPSR (sstatus) for clone
	MOV	256(X2), T0		// sstatus from frame
	ADD	$-16, X2
	MOV	T0, 8(X2)
	CALL	·SetSyscallSPSR(SB)
	ADD	$16, X2

	// Dispatch syscall: SyscallDispatch(num, a0, a1, a2, a3, a4, a5)
	// RISC-V syscall convention: a7=number, a0-a5=args
	MOV	128(X2), T0		// a7 from frame = syscall number
	MOV	72(X2), T1		// a0 from frame
	MOV	80(X2), T2		// a1
	MOV	88(X2), A3		// a2
	MOV	96(X2), A4		// a3
	MOV	104(X2), A5		// a4
	MOV	112(X2), A6		// a5

	ADD	$-72, X2		// 7 args + 1 return
	MOV	T0, 8(X2)		// syscallNum
	MOV	T1, 16(X2)		// arg0
	MOV	T2, 24(X2)		// arg1
	MOV	A3, 32(X2)		// arg2
	MOV	A4, 40(X2)		// arg3
	MOV	A5, 48(X2)		// arg4
	MOV	A6, 56(X2)		// arg5
	CALL	·SyscallDispatch(SB)
	MOV	64(X2), T0		// return value
	ADD	$72, X2

	// Store return value in saved a0 slot
	MOV	T0, 72(X2)		// a0 = return value

	// Check context switch
	ADD	$-16, X2
	CALL	·GetSyscallSwitchTarget(SB)
	MOV	8(X2), T0		// target or -1
	ADD	$16, X2

	MOV	$0, T1
	BLE	T0, T1, trap_return	// No switch

	// Context switch needed
	MOV	X2, T1			// frame pointer
	ADD	$-32, X2
	MOV	T1, 8(X2)		// framePtr
	MOV	T0, 16(X2)		// targetPtr
	CALL	·DoContextSwitch(SB)
	MOV	24(X2), S2		// new context pointer
	ADD	$32, X2

	BEQ	S2, ZERO, trap_return
	JMP	load_context_and_sret

handle_page_fault:
	// Read stval (fault address)
	// CSRR t1, stval
	WORD	$0x14302373		// csrr t1(x6), stval

	// Call HandlePageFaultAsm(faultAddr)
	ADD	$-24, X2
	MOV	T1, 8(X2)
	CALL	·HandlePageFaultAsm(SB)
	MOV	16(X2), T0		// handled?
	ADD	$24, X2

	BNE	T0, ZERO, trap_return

	// Try userspace handler
	// CSRR t1, stval
	WORD	$0x14302373
	ADD	$-24, X2
	MOV	T1, 8(X2)
	CALL	·HandleUserPageFaultAsm(SB)
	ADD	$24, X2

	JMP	trap_return

handle_interrupt:
	// Mask off interrupt bit to get cause code
	// t0 still has full scause
	MOV	$0x7FFFFFFFFFFFFFFF, T1
	AND	T0, T1, T0		// T0 = cause without interrupt bit

	// Timer interrupt = cause 5 (S-mode timer)
	MOV	$5, T1
	BEQ	T0, T1, handle_timer_interrupt

	// External interrupt = cause 9 (S-mode external)
	MOV	$9, T1
	BEQ	T0, T1, handle_external_interrupt

	// Unknown interrupt - return
	JMP	trap_return

handle_timer_interrupt:
	// Dispatch timer IRQ
	MOV	$5, T0			// IRQ number
	MOV	X2, T1			// frame pointer
	MOV	248(X2), T2		// sepc (ELR equivalent)
	MOV	8(X2), A3		// original sp

	ADD	$-72, X2
	MOV	T0, 8(X2)		// irqNum
	MOV	T1, 16(X2)		// framePtr
	MOV	T2, 24(X2)		// elr
	MOV	A3, 32(X2)		// sp
	CALL	·TimerIRQHandler(SB)
	ADD	$72, X2

	// Check thread preemption
	MOV	X2, T0			// frame pointer
	ADD	$-24, X2
	MOV	T0, 8(X2)
	CALL	·CheckThreadPreemption(SB)
	MOV	16(X2), S2		// new context or 0
	ADD	$24, X2

	BEQ	S2, ZERO, trap_return
	JMP	load_context_and_sret

handle_external_interrupt:
	// Dispatch to top-half handler (sets pending flags and queues bottom-half)
	// TODO: Read PLIC claim register to get IRQ number, store in topHalfIRQNum
	// For now, just return - we'll implement PLIC support later
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

	// Restore original sp: load from frame[1], put in sscratch,
	// deallocate frame, swap back
	MOV	8(X2), T0		// original sp (but we already loaded t0=x5 above)
	// Actually t0 was overloaded. Re-read from frame.
	// We need to be careful here. X5 (t0) was already restored above.
	// Let's use the fact that sscratch should be the exception stack.

	// Deallocate frame
	ADD	$FRAME_SIZE, X2

	// Put exception stack pointer back in sscratch and restore user sp
	// CSRRW sp, sscratch, sp
	WORD	$0x14011173		// swap sp and sscratch

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
	MOV	144(S2), X19
	MOV	152(S2), X20
	MOV	160(S2), X21
	MOV	168(S2), X22
	MOV	176(S2), X23
	MOV	184(S2), X24
	MOV	192(S2), X25
	MOV	200(S2), X26
	MOV	208(X18), g		// g
	MOV	216(S2), X28
	MOV	224(S2), X29
	MOV	232(S2), X30
	MOV	248(S2), X31

	// Restore sp via sscratch swap
	MOV	16(S2), A0		// target sp
	WORD	$0x14051073		// csrw sscratch, a0
	MOV	72(S2), X10		// reload a0
	MOV	136(S2), X18		// load s2
	WORD	$0x14011173		// csrrw sp, sscratch, sp

	// SRET
	WORD	$0x10200073
