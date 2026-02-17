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

// DumpInstructionPageFaultAsm tail-call stub
// NOSPLIT: called from exception stack for instruction page fault diagnostics
TEXT ·DumpInstructionPageFaultAsm(SB), NOSPLIT, $0-8
	JMP	·dumpInstructionPageFaultInternal(SB)

// HandlePageFaultAsm tail-call stub
// NOSPLIT: called from exception stack, must not trigger stack growth check
TEXT ·HandlePageFaultAsm(SB), NOSPLIT, $0-16
	JMP	·handlePageFaultInternal(SB)

// HandleUserPageFaultAsm tail-call stub
// NOSPLIT: called from exception stack, must not trigger stack growth check
TEXT ·HandleUserPageFaultAsm(SB), NOSPLIT, $0-16
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

// SetSyscallCloneRegs is a no-op on RISC-V (clone stores on child stack).
TEXT ·SetSyscallCloneRegs(SB), NOSPLIT, $0-24
	RET

// CheckThreadPreemption tail-call stub
TEXT ·CheckThreadPreemption(SB), NOSPLIT, $0-16
	JMP	·checkThreadPreemptionInternal(SB)

// ThreadExitAsm tail-call stub
// Called from exception handler to kill faulting user thread.
// Returns pointer to next ThreadContext (0 if no threads left).
TEXT ·ThreadExitAsm(SB), NOSPLIT, $0-8
	JMP	·threadExitInternal(SB)

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

// PLICDispatchIRQ tail-call stub - ABI0 entry point for PLIC external IRQ dispatch.
// Called from handle_external_interrupt via GO_CALL_0_0.
TEXT ·PLICDispatchIRQ(SB), NOSPLIT, $0-0
	JMP	·plicDispatchIRQInternal(SB)

// EnableSEIE sets SEIE (bit 9) in the SIE CSR to enable
// S-mode external interrupts from the PLIC.
TEXT ·EnableSEIE(SB), NOSPLIT|NOFRAME, $0-0
	MOV	$0x200, A0		// SEIE = bit 9
	// CSRS sie, a0
	WORD	$0x10452073		// csrs sie, a0
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
	ADD	$-16, X2
	CALL	·StartFirstThread(SB)
	MOV	8(X2), X18		// X18 = ThreadContext pointer
	ADD	$16, X2

	// Save exception stack from sscratch into T0 (X5).
	// We need to preserve it so sscratch points to the exception stack
	// after SRET, not to the Go stack.
	WORD	$0x140022F3		// csrr x5(t0), sscratch

	// Load SEPC and SSTATUS into CSRs
	MOV	256(X18), A0
	WORD	$0x14151073		// csrw sepc, a0
	MOV	264(X18), A0
	WORD	$0x10051073		// csrw sstatus, a0
	WORD	$0x12000073		// sfence.vma

	// Load all GPRs from context except T0(X5), SP(X2), S2(X18)
	MOV	8(X18), X1		// ra
	// Skip X2 (sp) - load near end
	MOV	24(X18), X3		// gp
	MOV	32(X18), TP		// tp
	// Skip X5 (t0) - holds exception stack, reload after sscratch restore
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
	// Skip X18 - context pointer
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

	// Restore sscratch = exception stack (from T0/X5)
	WORD	$0x14029073		// csrw sscratch, x5(t0)

	// Now reload T0 from context (was used as temp)
	MOV	40(X18), X5		// t0

	// Load sp directly from context
	MOV	16(X18), X2		// sp (changes stack, but we don't need it)

	// Load S2/X18 last (loses context pointer)
	MOV	144(X18), X18		// s2

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

	// Save SSTATUS with SPP=1 (return to S-mode) and SPIE=1 (enable IRQs on SRET)
	// SPP (bit 8) = 0x100: SRET returns to S-mode (NOT U-mode!)
	// SPIE (bit 5) = 0x020: SIE will be set to 1 after SRET
	MOV	$0x120, A0		// SPP | SPIE
	MOV	A0, 264(X18)		// sstatus

	// Call SaveThread0AndYield to get next thread's context
	ADD	$-16, X2
	CALL	·SaveThread0AndYield(SB)
	MOV	8(X2), X18		// X18 = new context (or 0)
	ADD	$16, X2

	BEQ	X18, ZERO, yield_restore_return

	// Save exception stack from sscratch into T0 (X5).
	// CRITICAL: sscratch must point to the exception stack after SRET,
	// not to the Go stack. Without this, the next trap handler would
	// use the Go stack as exception stack, causing corruption.
	WORD	$0x140022F3		// csrr x5(t0), sscratch

	// Load SEPC and SSTATUS into CSRs
	MOV	256(X18), A0
	WORD	$0x14151073		// csrw sepc, a0
	MOV	264(X18), A0
	WORD	$0x10051073		// csrw sstatus, a0
	WORD	$0x12000073		// sfence.vma

	// Load all GPRs except T0(X5), SP(X2), S2(X18)
	MOV	8(X18), X1		// ra
	MOV	24(X18), X3		// gp
	MOV	32(X18), TP		// tp
	// Skip X5 (t0) - holds exception stack
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
	MOV	152(X18), X19		// s3
	MOV	160(X18), X20		// s4
	MOV	168(X18), X21		// s5
	MOV	176(X18), X22		// s6
	MOV	184(X18), X23		// s7
	MOV	192(X18), X24		// s8
	MOV	200(X18), X25		// s9
	MOV	208(X18), X26		// s10
	MOV	216(X18), g		// g
	MOV	224(X18), X28		// t3
	MOV	232(X18), X29		// t4
	MOV	240(X18), X30		// t5
	MOV	248(X18), X31		// t6

	// Restore sscratch = exception stack (from T0/X5)
	WORD	$0x14029073		// csrw sscratch, x5(t0)

	// Reload T0 from context
	MOV	40(X18), X5		// t0

	// Load sp directly from context
	MOV	16(X18), X2		// sp

	// Load S2/X18 last (loses context pointer)
	MOV	144(X18), X18		// s2

	// SRET to new thread
	WORD	$0x10200073

yield_restore_return:
	RET

yield_no_thread:
	RET
