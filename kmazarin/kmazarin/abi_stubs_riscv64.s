//go:build !test_stubs

// abi_stubs_riscv64.s - ABI0 entry points and context switch for RISC-V
//
// ThreadContext layout (X[32] + SEPC + SSTATUS):
//   X[0] at offset 0 (unused, x0 is zero)
//   X[1] at offset 8 (ra), X[2] at offset 16 (sp), ...
//   X[27] at offset 216 (s11 = g register)
//   X[31] at offset 248 (t6)
//   SEPC at offset 256
//   SSTATUS at offset 264

#include "textflag.h"

// SyscallDispatch tail-call stub
TEXT ·SyscallDispatch(SB), NOSPLIT, $0-64
	JMP	·syscallDispatchInternal(SB)

// IRQDispatch tail-call stub
TEXT ·IRQDispatch(SB), NOSPLIT, $0-64
	JMP	·irqDispatchInternal(SB)

// TimerIRQHandler tail-call stub
TEXT ·TimerIRQHandler(SB), NOSPLIT, $0-64
	JMP	·timerIRQHandlerInternal(SB)

// HandlePageFaultAsm tail-call stub
TEXT ·HandlePageFaultAsm(SB), $0-16
	JMP	·handlePageFaultInternal(SB)

// HandleUserPageFaultAsm tail-call stub
TEXT ·HandleUserPageFaultAsm(SB), $0-16
	JMP	·handleUserPageFaultInternal(SB)

// GetSyscallSwitchTarget - must use CALL (has return value)
TEXT ·GetSyscallSwitchTarget(SB), NOSPLIT, $16-8
	CALL	·getSyscallSwitchTargetInternal(SB)
	MOV	8(X2), A0
	MOV	A0, ret+0(FP)
	RET

// DoContextSwitch - must use CALL (has return value)
TEXT ·DoContextSwitch(SB), NOSPLIT, $32-24
	MOV	framePtr+0(FP), A0
	MOV	targetPtr+8(FP), A1
	MOV	A0, 8(X2)
	MOV	A1, 16(X2)
	CALL	·doContextSwitchABI0(SB)
	MOV	24(X2), A0
	MOV	A0, ret+16(FP)
	RET

// SetSyscallELR tail-call stub
TEXT ·SetSyscallELR(SB), NOSPLIT, $0-8
	JMP	·setSyscallELRInternal(SB)

// SetSyscallSPSR tail-call stub
TEXT ·SetSyscallSPSR(SB), NOSPLIT, $0-8
	JMP	·setSyscallSPSRInternal(SB)

// CheckThreadPreemption tail-call stub
TEXT ·CheckThreadPreemption(SB), NOSPLIT, $0-16
	JMP	·checkThreadPreemptionInternal(SB)

// ============================================================================
// Exception vector setup
// ============================================================================

// GetExceptionVectorBase returns the address of the trap handler.
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	MOV	$·trapEntry(SB), A0
	MOV	A0, ret+0(FP)
	RET

// ExceptionVectorTable stub - RISC-V doesn't use a vector table,
// but this symbol must exist for cross-architecture compatibility.
// The actual trap entry is trapEntry in exceptions_riscv64.s.
TEXT ·ExceptionVectorTable(SB), NOSPLIT|NOFRAME, $0-0
	RET

// SetVBAR writes the stvec CSR (trap vector base address).
TEXT ·SetVBAR(SB), NOSPLIT, $0-8
	MOV	addr+0(FP), A0
	// CSRW stvec, a0
	WORD	$0x10551073
	RET

// ReadVBAR reads the stvec CSR.
TEXT ·ReadVBAR(SB), NOSPLIT, $0-8
	// CSRR a0, stvec
	WORD	$0x10502573
	MOV	A0, ret+0(FP)
	RET

// EnableIRQs sets sstatus.SIE to enable S-mode interrupts.
TEXT ·EnableIRQs(SB), NOSPLIT|NOFRAME, $0-0
	// CSRSI sstatus, 2 (set SIE bit)
	WORD	$0x10016073
	RET

// DisableIRQs clears sstatus.SIE to disable S-mode interrupts.
TEXT ·DisableIRQs(SB), NOSPLIT|NOFRAME, $0-0
	// CSRCI sstatus, 2 (clear SIE bit)
	WORD	$0x10017073
	RET

// SaveAndDisableIRQs reads sstatus, clears SIE, returns old sstatus.
TEXT ·SaveAndDisableIRQs(SB), NOSPLIT, $0-8
	// CSRR a0, sstatus
	WORD	$0x10002573
	// CSRCI sstatus, 2
	WORD	$0x10017073
	MOV	A0, ret+0(FP)
	RET

// RestoreIRQs restores sstatus from saved value.
TEXT ·RestoreIRQs(SB), NOSPLIT, $0-8
	MOV	savedDAIF+0(FP), A0
	// CSRW sstatus, a0
	WORD	$0x10051073
	RET

// GetGRegister returns g (X27/s11, Go's g register on riscv64).
TEXT ·GetGRegister(SB), NOSPLIT|NOFRAME, $0-8
	MOV	g, A0
	MOV	A0, ret+0(FP)
	RET

// GetPC returns the caller's return address (RA).
TEXT ·GetPC(SB), NOSPLIT, $0-8
	MOV	RA, A0
	MOV	A0, ret+0(FP)
	RET

// asyncPreemptWrapper placeholder
TEXT ·asyncPreemptWrapper(SB), NOSPLIT|NOFRAME, $0-0
	RET

// getAsyncPreemptWrapperAddr returns address of asyncPreemptWrapper.
TEXT ·getAsyncPreemptWrapperAddr(SB), NOSPLIT, $0-8
	MOV	$·asyncPreemptWrapper(SB), A0
	MOV	A0, ret+0(FP)
	RET

// ============================================================================
// RunFirstThread - Load context and SRET to first thread
// ============================================================================
TEXT ·RunFirstThread(SB), NOSPLIT|NOFRAME, $0-0
	// Call StartFirstThread to get ThreadContext pointer
	// Allocate call frame
	ADD	$-16, X2
	CALL	·StartFirstThread(SB)
	MOV	8(X2), X18		// X18 = ThreadContext pointer
	ADD	$16, X2

	// Load SEPC (offset 256) into sepc CSR
	MOV	256(X18), A0
	// CSRW sepc, a0
	WORD	$0x14151073

	// Load SSTATUS (offset 264) into sstatus CSR
	MOV	264(X18), A0
	// CSRW sstatus, a0
	WORD	$0x10051073

	// TLB flush
	// SFENCE.VMA zero,zero
	WORD	$0x12000073

	// Load all GPRs from ThreadContext
	// X[1]=ra at offset 8
	MOV	8(X18), X1		// ra
	MOV	16(X18), X2		// sp (WARNING: changes our stack!)
	// After loading sp, we can't access X18 via stack anymore
	// But X18 is still valid as a register

	// Actually, we need to load sp LAST since it changes our addressing.
	// Restore sp from X2 register. Let me reorder.

	// Load all GPRs except sp and X18 (our pointer)
	MOV	8(X18), X1		// ra
	// Skip X2 (sp) - load last
	MOV	24(X18), X3		// gp
	MOV	32(X18), TP		// tp
	MOV	40(X18), X5		// t0
	MOV	48(X18), X6		// t1
	MOV	56(X18), X7		// t2
	MOV	64(X18), X8		// s0
	MOV	72(X18), X9		// s1
	MOV	80(X18), X10		// a0
	MOV	88(X18), X11		// a1
	MOV	96(X18), X12		// a2
	MOV	104(X18), X13		// a3
	MOV	112(X18), X14		// a4
	MOV	120(X18), X15		// a5
	MOV	128(X18), X16		// a6
	MOV	136(X18), X17		// a7
	// Skip X18 (X18) - our pointer
	MOV	152(X18), X19		// s3
	MOV	160(X18), X20		// s4
	MOV	168(X18), X21		// s5
	MOV	176(X18), X22		// s6
	MOV	184(X18), X23		// s7
	MOV	192(X18), X24		// s8
	MOV	200(X18), X25		// s9
	MOV	208(X18), X26		// s10
	MOV	216(X18), g		// s11 (g register)
	MOV	224(X18), X28		// t3
	MOV	232(X18), X29		// t4
	MOV	240(X18), X30		// t5
	MOV	248(X18), X31		// t6

	// Load sp (X2) from context - do this after all memory loads
	// First save X18's target in a temp register we already loaded
	// Use sscratch to hold the context pointer temporarily
	// CSRW sscratch, x18
	WORD	$0x14091073		// csrw sscratch, x18 (x18)
	// Load X18 from context
	MOV	144(X18), X18		// Now X18 has its real value
	// Load sp from sscratch-saved pointer... but we can't anymore
	// Actually, we need a different approach.

	// Let's use a different strategy: load sp from the context using
	// a temporary register, then load that temp register last.
	// We already loaded all registers except sp and the temp.
	// Use sscratch to hold the sp value.

	// Undo: reload X18 from context pointer in sscratch
	// CSRR s2, sscratch
	WORD	$0x14002973		// csrr s2(x18), sscratch

	// Save desired sp value to sscratch
	MOV	16(X18), A0		// A0 = target sp
	// CSRW sscratch, a0
	WORD	$0x14051073		// csrw sscratch, a0

	// Reload A0 from context
	MOV	80(X18), X10		// a0

	// Load X18 from context
	MOV	144(X18), X18		// s2

	// Now load sp from sscratch
	// CSRRW sp, sscratch, sp (swap sp and sscratch)
	WORD	$0x14011173		// csrrw x2, sscratch, x2

	// SRET to thread
	WORD	$0x10200073		// SRET

	// Should never reach here
run_first_hang:
	WORD	$0x10500073		// WFI
	JMP	run_first_hang

// ============================================================================
// YieldToReadyThread - Save thread 0 context and switch
// ============================================================================
TEXT ·YieldToReadyThread(SB), NOSPLIT|NOFRAME, $0-0
	// X18 = pointer to current thread's ThreadContext
	MOV	·CurrentThread(SB), X18
	BEQ	X18, ZERO, yield_no_thread
	MOV	·ThreadContextOffset(SB), A0
	ADD	A0, X18, X18		// X18 = &Thread.Context

	// Save all GPRs to ThreadContext
	MOV	X1, 8(X18)		// ra
	MOV	X2, 16(X18)		// sp
	MOV	X3, 24(X18)		// gp
	MOV	TP, 32(X18)		// tp
	MOV	X5, 40(X18)		// t0
	MOV	X6, 48(X18)		// t1
	MOV	X7, 56(X18)		// t2
	MOV	X8, 64(X18)		// s0
	MOV	X9, 72(X18)		// s1
	MOV	X10, 80(X18)		// a0
	MOV	X11, 88(X18)		// a1
	MOV	X12, 96(X18)		// a2
	MOV	X13, 104(X18)		// a3
	MOV	X14, 112(X18)		// a4
	MOV	X15, 120(X18)		// a5
	MOV	X16, 128(X18)		// a6
	MOV	X17, 136(X18)		// a7
	// X18 (X18) = our pointer, save 0
	MOV	ZERO, 144(X18)
	MOV	X19, 152(X18)		// s3
	MOV	X20, 160(X18)		// s4
	MOV	X21, 168(X18)		// s5
	MOV	X22, 176(X18)		// s6
	MOV	X23, 184(X18)		// s7
	MOV	X24, 192(X18)		// s8
	MOV	X25, 200(X18)		// s9
	MOV	X26, 208(X18)		// s10
	MOV	g, 216(X18)		// s11 (g)
	MOV	X28, 224(X18)		// t3
	MOV	X29, 232(X18)		// t4
	MOV	X30, 240(X18)		// t5
	MOV	X31, 248(X18)		// t6

	// Save SEPC = RA (return address for when thread 0 is scheduled back)
	MOV	X1, 256(X18)		// sepc = ra

	// Save SSTATUS with SPIE set (enable interrupts on SRET)
	MOV	$0x20, A0		// SPIE bit
	MOV	A0, 264(X18)		// sstatus

	// Call SaveThread0AndYield to get next thread's context
	ADD	$-16, X2
	CALL	·SaveThread0AndYield(SB)
	MOV	8(X2), X18		// X18 = new context (or 0)
	ADD	$16, X2

	BEQ	X18, ZERO, yield_restore_return

	// Load SEPC from new context
	MOV	256(X18), A0
	// CSRW sepc, a0
	WORD	$0x14151073

	// Load SSTATUS from new context
	MOV	264(X18), A0
	// CSRW sstatus, a0
	WORD	$0x10051073

	// TLB flush
	WORD	$0x12000073		// SFENCE.VMA

	// Load all GPRs (same pattern as RunFirstThread)
	MOV	8(X18), X1		// ra
	MOV	24(X18), X3
	MOV	32(X18), TP
	MOV	40(X18), X5
	MOV	48(X18), X6
	MOV	56(X18), X7
	MOV	64(X18), X8
	MOV	72(X18), X9
	MOV	80(X18), X10
	MOV	88(X18), X11
	MOV	96(X18), X12
	MOV	104(X18), X13
	MOV	112(X18), X14
	MOV	120(X18), X15
	MOV	128(X18), X16
	MOV	136(X18), X17
	MOV	152(X18), X19
	MOV	160(X18), X20
	MOV	168(X18), X21
	MOV	176(X18), X22
	MOV	184(X18), X23
	MOV	192(X18), X24
	MOV	200(X18), X25
	MOV	208(X18), X26
	MOV	216(X18), g		// g
	MOV	224(X18), X28
	MOV	232(X18), X29
	MOV	240(X18), X30
	MOV	248(X18), X31

	// Load sp via sscratch swap
	MOV	16(X18), A0
	WORD	$0x14051073		// csrw sscratch, a0
	MOV	80(X18), X10		// reload a0
	MOV	144(X18), X18		// load s2
	WORD	$0x14011173		// csrrw x2, sscratch, x2

	// SRET to new thread
	WORD	$0x10200073

yield_restore_return:
	RET

yield_no_thread:
	RET
