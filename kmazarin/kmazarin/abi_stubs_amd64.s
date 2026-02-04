//go:build !test_stubs

// abi_stubs_amd64.s - ABI0 entry points and context switch for x86_64
//
// Exception frame layout (pushed by handler, from SP upward):
//   [0]=RAX [1]=RBX [2]=RCX [3]=RDX [4]=RSI [5]=RDI [6]=RBP
//   [7]=R8 [8]=R9 [9]=R10 [10]=R11 [11]=R12 [12]=R13 [13]=R14 [14]=R15
//   [15]=error_code [16]=RIP [17]=CS [18]=RFLAGS [19]=RSP [20]=SS
//
// ThreadContext layout:
//   0=RAX 8=RBX 16=RCX 24=RDX 32=RSI 40=RDI 48=RBP
//   56=R8 64=R9 72=R10 80=R11 88=R12 96=R13 104=R14(g) 112=R15
//   120=RIP 128=RFLAGS 136=RSP

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
	MOVQ	8(SP), AX
	MOVQ	AX, ret+0(FP)
	RET

// DoContextSwitch - must use CALL (has return value)
TEXT ·DoContextSwitch(SB), NOSPLIT, $32-24
	MOVQ	framePtr+0(FP), AX
	MOVQ	targetPtr+8(FP), BX
	MOVQ	AX, 8(SP)
	MOVQ	BX, 16(SP)
	CALL	·doContextSwitchABI0(SB)
	MOVQ	24(SP), AX
	MOVQ	AX, ret+16(FP)
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
// Exception vector table setup
// ============================================================================

// GetExceptionVectorBase returns the address of the IDT setup routine.
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	LEAQ	·ExceptionVectorTable(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// ExceptionVectorTable is a placeholder for the IDT.
// On x86_64, the actual IDT is built by Go code in SetupIDT().
TEXT ·ExceptionVectorTable(SB), NOSPLIT|NOFRAME, $0-0
	RET

// SetVBAR loads the IDT register (LIDT).
// addr points to a 10-byte IDT descriptor: [limit:16][base:64]
TEXT ·SetVBAR(SB), NOSPLIT, $0-8
	MOVQ	addr+0(FP), AX
	// LIDT expects a 10-byte descriptor in memory
	// The caller must have set up the descriptor at this address
	BYTE	$0x0F; BYTE $0x01; BYTE $0x18	// LIDT [RAX]
	RET

// ReadVBAR reads the IDT register (SIDT).
TEXT ·ReadVBAR(SB), NOSPLIT, $16-8
	// SIDT stores 10 bytes at [RSP]: 2-byte limit + 8-byte base
	BYTE	$0x0F; BYTE $0x01; BYTE $0x0C; BYTE $0x24	// SIDT [RSP]
	MOVQ	2(SP), AX	// base address starts at offset 2
	MOVQ	AX, ret+0(FP)
	RET

// EnableIRQs enables interrupts (STI).
TEXT ·EnableIRQs(SB), NOSPLIT|NOFRAME, $0-0
	STI
	RET

// DisableIRQs disables interrupts (CLI).
TEXT ·DisableIRQs(SB), NOSPLIT|NOFRAME, $0-0
	CLI
	RET

// SaveAndDisableIRQs saves RFLAGS and disables interrupts.
TEXT ·SaveAndDisableIRQs(SB), NOSPLIT, $0-8
	PUSHFQ
	POPQ	AX
	CLI
	MOVQ	AX, ret+0(FP)
	RET

// RestoreIRQs restores RFLAGS from saved value.
TEXT ·RestoreIRQs(SB), NOSPLIT, $0-8
	MOVQ	savedDAIF+0(FP), AX
	PUSHQ	AX
	POPFQ
	RET

// GetGRegister returns R14 (Go's g register on amd64).
TEXT ·GetGRegister(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ	R14, ret+0(FP)
	RET

// GetPC returns the caller's return address.
TEXT ·GetPC(SB), NOSPLIT, $0-8
	MOVQ	0(SP), AX		// Return address on stack
	MOVQ	AX, ret+0(FP)
	RET

// asyncPreemptWrapper saves g around asyncPreempt calls.
TEXT ·asyncPreemptWrapper(SB), NOSPLIT|NOFRAME, $0-0
	RET

// getAsyncPreemptWrapperAddr returns address of asyncPreemptWrapper.
TEXT ·getAsyncPreemptWrapperAddr(SB), NOSPLIT, $0-8
	LEAQ	·asyncPreemptWrapper(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// ============================================================================
// RunFirstThread - Load context and IRETQ to first thread
// ============================================================================
TEXT ·RunFirstThread(SB), NOSPLIT|NOFRAME, $0-0
	// Call StartFirstThread to get ThreadContext pointer
	SUBQ	$16, SP
	CALL	·StartFirstThread(SB)
	MOVQ	8(SP), R12		// R12 = ThreadContext pointer
	ADDQ	$16, SP

	// Flush TLB before switching
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Build IRETQ frame: SS, RSP, RFLAGS, CS, RIP (push in reverse order)
	PUSHQ	$0			// SS
	PUSHQ	136(R12)		// RSP from context
	PUSHQ	128(R12)		// RFLAGS from context
	PUSHQ	$0x08			// CS (kernel code segment)
	PUSHQ	120(R12)		// RIP from context

	// Load all GPRs from ThreadContext
	MOVQ	0(R12), AX		// RAX
	MOVQ	8(R12), BX		// RBX
	MOVQ	16(R12), CX		// RCX
	MOVQ	24(R12), DX		// RDX
	MOVQ	32(R12), SI		// RSI
	MOVQ	40(R12), DI		// RDI
	MOVQ	48(R12), BP		// RBP
	MOVQ	56(R12), R8
	MOVQ	64(R12), R9
	MOVQ	72(R12), R10
	MOVQ	80(R12), R11
	// Skip R12 (our context pointer) - load after R13-R15
	MOVQ	96(R12), R13
	MOVQ	104(R12), R14		// g register
	MOVQ	112(R12), R15

	// Load R12 LAST (was our context pointer)
	MOVQ	88(R12), R12

	IRETQ

	// Should never reach here
run_first_hang:
	HLT
	JMP	run_first_hang

// ============================================================================
// YieldToReadyThread - Save thread 0 context and switch to next thread
// ============================================================================
TEXT ·YieldToReadyThread(SB), NOSPLIT|NOFRAME, $0-0
	// R12 = pointer to current thread's ThreadContext
	MOVQ	·CurrentThread(SB), R12
	TESTQ	R12, R12
	JZ	yield_no_thread
	MOVQ	·ThreadContextOffset(SB), AX
	ADDQ	AX, R12			// R12 = &Thread.Context

	// Save all GPRs to ThreadContext
	MOVQ	AX, 0(R12)		// RAX (clobbered by offset load, but OK)
	MOVQ	BX, 8(R12)
	MOVQ	CX, 16(R12)
	MOVQ	DX, 24(R12)
	MOVQ	SI, 32(R12)
	MOVQ	DI, 40(R12)
	MOVQ	BP, 48(R12)
	MOVQ	R8, 56(R12)
	MOVQ	R9, 64(R12)
	MOVQ	R10, 72(R12)
	MOVQ	R11, 80(R12)
	// R12 is our context pointer — save 0 as placeholder
	MOVQ	$0, 88(R12)
	MOVQ	R13, 96(R12)
	MOVQ	R14, 104(R12)		// g register
	MOVQ	R15, 112(R12)

	// Save RIP = return address (pushed by CALL to us)
	MOVQ	0(SP), AX
	MOVQ	AX, 120(R12)		// RIP

	// Save RFLAGS with IF set (interrupts enabled when resumed)
	PUSHFQ
	POPQ	AX
	ORQ	$0x200, AX
	MOVQ	AX, 128(R12)		// RFLAGS

	// Save RSP (caller's RSP = our SP + 8 for the return address)
	LEAQ	8(SP), AX
	MOVQ	AX, 136(R12)		// RSP

	// Call SaveThread0AndYield() to get next thread's context
	SUBQ	$16, SP
	CALL	·SaveThread0AndYield(SB)
	MOVQ	8(SP), R12		// R12 = new context pointer (or 0)
	ADDQ	$16, SP

	TESTQ	R12, R12
	JZ	yield_restore_return

	// Switch to new thread via IRETQ
	// Flush TLB
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Build IRETQ frame
	PUSHQ	$0			// SS
	PUSHQ	136(R12)		// RSP
	PUSHQ	128(R12)		// RFLAGS
	PUSHQ	$0x08			// CS
	PUSHQ	120(R12)		// RIP

	// Load all GPRs
	MOVQ	0(R12), AX
	MOVQ	8(R12), BX
	MOVQ	16(R12), CX
	MOVQ	24(R12), DX
	MOVQ	32(R12), SI
	MOVQ	40(R12), DI
	MOVQ	48(R12), BP
	MOVQ	56(R12), R8
	MOVQ	64(R12), R9
	MOVQ	72(R12), R10
	MOVQ	80(R12), R11
	MOVQ	96(R12), R13
	MOVQ	104(R12), R14
	MOVQ	112(R12), R15
	MOVQ	88(R12), R12		// Load R12 last

	IRETQ

yield_restore_return:
	// No thread to switch to — return normally
	RET

yield_no_thread:
	RET
