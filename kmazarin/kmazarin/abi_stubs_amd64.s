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
//   120=RIP 128=RFLAGS 136=RSP 144=FSBase

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
TEXT ·HandlePageFaultAsm(SB), NOSPLIT, $0-16
	JMP	·handlePageFaultInternal(SB)

// HandleUserPageFaultAsm tail-call stub
// Go signature: func HandleUserPageFaultAsm(faultAddr, isPermFault uint64) uint64
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
TEXT ·HandleUserPageFaultAsm(SB), NOSPLIT, $0-24
	JMP	·handleUserPageFaultInternal(SB)

// GetSyscallSwitchTarget - must use CALL (has return value)
// x86_64: CALL pushes return addr, so return value lands at 0(SP) not 8(SP)
TEXT ·GetSyscallSwitchTarget(SB), NOSPLIT, $16-8
	CALL	·getSyscallSwitchTargetInternal(SB)
	MOVQ	0(SP), AX
	MOVQ	AX, ret+0(FP)
	RET

// DoContextSwitch - must use CALL (has return value)
// x86_64: CALL pushes return addr, so args at 0(SP), 8(SP); return at 16(SP)
TEXT ·DoContextSwitch(SB), NOSPLIT, $32-24
	MOVQ	framePtr+0(FP), AX
	MOVQ	targetPtr+8(FP), BX
	MOVQ	AX, 0(SP)
	MOVQ	BX, 8(SP)
	CALL	·doContextSwitchABI0(SB)
	MOVQ	16(SP), AX
	MOVQ	AX, ret+16(FP)
	RET

// SetSyscallELR tail-call stub
TEXT ·SetSyscallELR(SB), NOSPLIT, $0-8
	JMP	·setSyscallELRInternal(SB)

// SetSyscallSPSR tail-call stub
TEXT ·SetSyscallSPSR(SB), NOSPLIT, $0-8
	JMP	·setSyscallSPSRInternal(SB)

// SetSyscallCloneRegs tail-call stub (3 args: r12, r13, r9)
TEXT ·SetSyscallCloneRegs(SB), NOSPLIT, $0-24
	JMP	·setSyscallCloneRegsInternal(SB)

// CheckThreadPreemption tail-call stub
TEXT ·CheckThreadPreemption(SB), NOSPLIT, $0-16
	JMP	·checkThreadPreemptionInternal(SB)

// ThreadExitAsm tail-call stub
// Called from exception handler to kill faulting user thread.
// Returns pointer to next ThreadContext (0 if no threads left).
TEXT ·ThreadExitAsm(SB), NOSPLIT, $0-8
	JMP	·threadExitInternal(SB)

// TerminatePriestAsm tail-call stub
// Go signature: func TerminatePriestAsm(pid uint64, status int64) uint64
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
TEXT ·TerminatePriestAsm(SB), NOSPLIT, $0-24
	JMP	·terminatePriestInternal(SB)

// HandleUnhandledExceptionAsm tail-call stub
// Go signature: func HandleUnhandledExceptionAsm(excInfo, faultAddr, faultPC uint64) uint64
// ABI0: 3 args (24 bytes) + 1 return (8 bytes) = 32 bytes
// NOT NOSPLIT: exception stack is above g0.stackguard0.
TEXT ·HandleUnhandledExceptionAsm(SB), $0-32
	JMP	·handleUnhandledExceptionInternal(SB)

// ============================================================================
// Exception vector table setup
// ============================================================================

// GetExceptionVectorBase builds the IDT and returns the IDTR descriptor address.
// On x86_64, we build a real IDT with interrupt gate entries, then return
// the address of the IDTR descriptor (10 bytes: limit + base) for LIDT.
// Uses tail-call to getExceptionVectorBaseInternal which does the real work.
//
// NOTE: The LEAQ of ExceptionVectorTable is a dead reference to keep the symbol
// in the ELF — diplomat resolves it at load time.
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	LEAQ	·ExceptionVectorTable(SB), AX	// keep symbol alive for diplomat
	JMP	·getExceptionVectorBaseInternal(SB)

// ExceptionVectorTable is a marker symbol for diplomat's ELF symbol lookup.
// On x86_64, the actual IDT is built dynamically by BuildIDT().
// This stub exists solely so diplomat can find the symbol in kmazarin's ELF.
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


// ============================================================================
// RunFirstThread - Load context and IRETQ to first thread
// ============================================================================
TEXT ·RunFirstThread(SB), NOSPLIT|NOFRAME, $0-0
	// Call StartFirstThread to get ThreadContext pointer
	// x86_64: CALL pushes return addr, so return value at 0(SP)
	SUBQ	$16, SP
	CALL	·StartFirstThread(SB)
	MOVQ	0(SP), R12		// R12 = ThreadContext pointer
	ADDQ	$16, SP

	// Flush TLB before switching
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Restore FS_BASE from context (per-thread TLS base).
	// CRITICAL: If FSBase==0 (e.g., fresh userspace thread), skip BOTH
	// the WRMSR and the TLS sync write.
	MOVQ	144(R12), AX		// FSBase
	TESTQ	AX, AX
	JZ	run_skip_tls		// FSBase==0 → skip WRMSR AND TLS sync
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR

	// Sync TLS: write g to FS_BASE - 8
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = FS_BASE
	MOVQ	104(R12), DX		// new g (R14)
	MOVQ	DX, -8(AX)		// Write g to TLS slot
run_skip_tls:

	// Build IRETQ frame: SS, RSP, RFLAGS, CS, RIP (push in reverse order)
	PUSHQ	160(R12)		// SS from context
	PUSHQ	136(R12)		// RSP from context
	PUSHQ	128(R12)		// RFLAGS from context
	PUSHQ	152(R12)		// CS from context
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
	// Save XMM registers BEFORE calling Go code (which clobbers them).
	// Matches common_exception_entry's XMM save — ensures the interrupted
	// thread's XMM state is preserved across the Go scheduler call.
	MOVOU	X0, ·xmmSaveArea+0(SB)
	MOVOU	X1, ·xmmSaveArea+16(SB)
	MOVOU	X2, ·xmmSaveArea+32(SB)
	MOVOU	X3, ·xmmSaveArea+48(SB)
	MOVOU	X4, ·xmmSaveArea+64(SB)
	MOVOU	X5, ·xmmSaveArea+80(SB)
	MOVOU	X6, ·xmmSaveArea+96(SB)
	MOVOU	X7, ·xmmSaveArea+112(SB)
	MOVOU	X8, ·xmmSaveArea+128(SB)
	MOVOU	X9, ·xmmSaveArea+144(SB)
	MOVOU	X10, ·xmmSaveArea+160(SB)
	MOVOU	X11, ·xmmSaveArea+176(SB)
	MOVOU	X12, ·xmmSaveArea+192(SB)
	MOVOU	X13, ·xmmSaveArea+208(SB)
	MOVOU	X14, ·xmmSaveArea+224(SB)
	MOVOU	X15, ·xmmSaveArea+240(SB)

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

	// Save RFLAGS as-is (preserve current IF state).
	// During early boot (before EnableIRQs), IF=0 — forcing IF=1 here would
	// enable interrupts prematurely after IRETQ, causing spurious interrupt
	// crashes. After EnableIRQs, IF=1 naturally, so timer preemption works.
	PUSHFQ
	POPQ	AX
	MOVQ	AX, 128(R12)		// RFLAGS

	// Save RSP (caller's RSP = our SP + 8 for the return address)
	LEAQ	8(SP), AX
	MOVQ	AX, 136(R12)		// RSP

	// Save FS_BASE MSR (per-thread TLS base)
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, 144(R12)		// FSBase

	// Save CS/SS — kernel context
	MOVQ	$0x08, 152(R12)		// CS = kernelCS
	MOVQ	$0x10, 160(R12)		// SS = kernelSS

	// Call SaveThread0AndYield() to get next thread's context
	SUBQ	$16, SP
	CALL	·SaveThread0AndYield(SB)
	MOVQ	0(SP), R12		// R12 = new context pointer (or 0)
	ADDQ	$16, SP

	TESTQ	R12, R12
	JZ	yield_restore_return

	// Flush TLB
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Restore FS_BASE from context (per-thread TLS base).
	MOVQ	144(R12), AX		// FSBase
	TESTQ	AX, AX
	JZ	yield_skip_tls		// FSBase==0 → skip WRMSR AND TLS sync
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR

	// Sync TLS: write g to FS_BASE - 8
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = FS_BASE
	MOVQ	104(R12), DX		// new g (R14)
	MOVQ	DX, -8(AX)		// Write g to TLS slot
yield_skip_tls:

	// Check for corrupted RIP
	MOVQ	120(R12), AX
	CMPQ	AX, $0x100000
	JAE	yr_rip_ok
	MOVW	$0x3F8, DX
	MOVB	$'!', AX; OUTB
	MOVB	$'Y', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	120(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
yr_halt:
	HLT
	JMP	yr_halt
yr_rip_ok:
	// Restore XMM registers (saved at entry before Go scheduler call)
	MOVOU	·xmmSaveArea+0(SB), X0
	MOVOU	·xmmSaveArea+16(SB), X1
	MOVOU	·xmmSaveArea+32(SB), X2
	MOVOU	·xmmSaveArea+48(SB), X3
	MOVOU	·xmmSaveArea+64(SB), X4
	MOVOU	·xmmSaveArea+80(SB), X5
	MOVOU	·xmmSaveArea+96(SB), X6
	MOVOU	·xmmSaveArea+112(SB), X7
	MOVOU	·xmmSaveArea+128(SB), X8
	MOVOU	·xmmSaveArea+144(SB), X9
	MOVOU	·xmmSaveArea+160(SB), X10
	MOVOU	·xmmSaveArea+176(SB), X11
	MOVOU	·xmmSaveArea+192(SB), X12
	MOVOU	·xmmSaveArea+208(SB), X13
	MOVOU	·xmmSaveArea+224(SB), X14
	MOVOU	·xmmSaveArea+240(SB), X15

	// Build IRETQ frame on current stack (same pattern as load_context_and_iretq)
	LEAQ	-40(SP), SP		// make room for 5 QWORDs
	MOVQ	160(R12), AX		// SS
	MOVQ	AX, 32(SP)
	MOVQ	136(R12), AX		// RSP
	MOVQ	AX, 24(SP)
	MOVQ	128(R12), AX		// RFLAGS
	MOVQ	AX, 16(SP)
	MOVQ	152(R12), AX		// CS
	MOVQ	AX, 8(SP)
	MOVQ	120(R12), AX		// RIP
	MOVQ	AX, 0(SP)

	// Load all GPRs from context
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
	// No thread to switch to — restore XMM and return normally
	MOVOU	·xmmSaveArea+0(SB), X0
	MOVOU	·xmmSaveArea+16(SB), X1
	MOVOU	·xmmSaveArea+32(SB), X2
	MOVOU	·xmmSaveArea+48(SB), X3
	MOVOU	·xmmSaveArea+64(SB), X4
	MOVOU	·xmmSaveArea+80(SB), X5
	MOVOU	·xmmSaveArea+96(SB), X6
	MOVOU	·xmmSaveArea+112(SB), X7
	MOVOU	·xmmSaveArea+128(SB), X8
	MOVOU	·xmmSaveArea+144(SB), X9
	MOVOU	·xmmSaveArea+160(SB), X10
	MOVOU	·xmmSaveArea+176(SB), X11
	MOVOU	·xmmSaveArea+192(SB), X12
	MOVOU	·xmmSaveArea+208(SB), X13
	MOVOU	·xmmSaveArea+224(SB), X14
	MOVOU	·xmmSaveArea+240(SB), X15
	RET

yield_no_thread:
	RET
