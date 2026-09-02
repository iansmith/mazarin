// abi_stubs_amd64.s - ABI0 entry points and context switch for x86_64
//
// Exception frame layout (pushed by handler, from SP upward):
//   [0]=RAX [1]=RBX [2]=RCX [3]=RDX [4]=RSI [5]=RDI [6]=RBP
//   [7]=R8 [8]=R9 [9]=R10 [10]=R11 [11]=R12 [12]=R13 [13]=R14 [14]=R15
//   [15]=error_code [16]=RIP [17]=CS [18]=RFLAGS [19]=RSP [20]=SS
//
// ThreadContext fields are accessed by symbolic offset (ThreadContext_RAX, ...)
// emitted into go_asm.h from the Go struct in thread_context_amd64.go. The
// access vocabulary (FST/FLD/CTX_GPRS) lives in frame_dsl_amd64.h. There are no
// numeric ctx offsets here — reorder the struct and these accesses follow.

#include "textflag.h"
#include "frame_dsl_amd64.h"

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

// PriorityWakeSwitch tail-call stub (MAZ-141 priority-wake diagnostic).
// Drop-in for CheckThreadPreemption at device_pwake_allowed: records the
// interrupted context around the identical scheduler switch.
TEXT ·PriorityWakeSwitch(SB), NOSPLIT, $0-16
	JMP	·priorityWakeSwitchInternal(SB)

// CheckKernelGoroutinePreempt checks kernel goroutine async preemption.
// NOT NOSPLIT: calls isAsyncSafePoint (exception stack > g0.stackguard0).
TEXT ·CheckKernelGoroutinePreempt(SB), $0-16
	JMP	·checkKernelGoroutinePreemptInternal(SB)

// ThreadExitAsm tail-call stub
// Called from exception handler to kill faulting user thread.
// Returns pointer to next ThreadContext (0 if no threads left).
TEXT ·ThreadExitAsm(SB), NOSPLIT, $0-8
	JMP	·threadExitInternal(SB)

// TerminateShepherdAsm tail-call stub
// Go signature: func TerminateShepherdAsm(pid uint64, status int64) uint64
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
TEXT ·TerminateShepherdAsm(SB), NOSPLIT, $0-24
	JMP	·terminateShepherdInternal(SB)

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
//
// Deliberately unpaired: the obvious partner DisableIRQs had no callers and was
// deleted with MAZ-167's other redundant DAIF/RFLAGS copies. Unconditional
// unmasking is a boot-time operation; masking is always save-and-restore, so it
// goes through ksync.SaveAndDisableIRQs instead.
TEXT ·EnableIRQs(SB), NOSPLIT|NOFRAME, $0-0
	STI
	RET

// SaveAndDisableIRQs / RestoreIRQs moved to ksync (MAZ-167). Go callers go
// through the wrappers in irq.go.

// GetGRegister returns R14 (Go's g register on amd64).
TEXT ·GetGRegister(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ	R14, ret+0(FP)
	RET

// GetPC returns the caller's return address.
TEXT ·GetPC(SB), NOSPLIT, $0-8
	MOVQ	0(SP), AX		// Return address on stack
	MOVQ	AX, ret+0(FP)
	RET

// readELR_EL1 returns the interrupted PC. ARM64 reads the ELR_EL1 system
// register; x86_64 has no equivalent, so common_exception_entry stashes the
// interrupted RIP (exception frame offset 128) into ·interruptedRIP on every
// exception entry. Same semantics as ELR_EL1: holds the most-recent exception's
// PC, valid until the next exception clobbers it. Used by the [SCHEDLK-VIOLATION]
// diagnostic to report the PC of whoever held schedulerLock with IRQs enabled.
// MAZ-139 DoD#2: ·interruptedRIP stays a single global (not per-exception-frame,
// unlike the TLS-g/vector) — this stub is NOFRAME with no framePtr, and a single
// most-recent PC is the faithful ELR_EL1 model. See the var doc in save_context_amd64.go.
TEXT ·readELR_EL1(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ	·interruptedRIP(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// inHandlerContextASM: ARM64 reads SPSel so pushStringFull can refuse to
// park from handler context, where a suspended frame on the shared
// exception stack cannot be legally resumed (MAZ-193). amd64 handler
// chains ARE resumable by design — the MAZ-136 global IST/RSP0 rotation
// cursors retire abandoned levels — so parking from handler context is
// legal here and this deliberately always reports "not handler context".
TEXT ·inHandlerContextASM(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ	$0, ret+0(FP)
	RET

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

	// FRAME-SAVE-BEGIN yield-to-ready
	// Save all GPRs to ThreadContext via the shared CTX_GPRS list. AX currently
	// holds ThreadContextOffset (not the interrupted RAX); storing it to the RAX
	// slot is the pre-existing behavior — a voluntary yield does not preserve the
	// caller's scratch RAX.
	CTX_GPRS(FST, R12)
	MOVQ	$0, ThreadContext_R12(R12)	// R12 is the base pointer — no live value to save
	// MAZ-135: also store the dual-home g into ctx.TLSG. amd64 keeps g in both
	// R14 and TLS ([FS_BASE-8]); load_context_and_iretq restores TLS-g from
	// ctx.TLSG, so a stale TLSG here would resurface "morestack on g0" at the
	// next stack growth. R14 is the authoritative g at a voluntary yield.
	FST(R12, ThreadContext_TLSG, R14)
	// MAZ-136 rotations: deliberately NO TSS.IST1 or TSS.RSP0 save here.
	// Both cursors are GLOBAL (never per-thread); Yield is a voluntary
	// CALL, not an exception, so no chain dies and there is nothing to
	// retire on either. See the IST ROTATION and RSP0 ROTATION banners in
	// exceptions_amd64.s before "fixing" this absence.

	// Save RIP = return address (pushed by CALL to us)
	MOVQ	0(SP), AX
	FST(R12, ThreadContext_RIP, AX)

	// Save RFLAGS as-is (preserve current IF state).
	// During early boot (before EnableIRQs), IF=0 — forcing IF=1 here would
	// enable interrupts prematurely after IRETQ, causing spurious interrupt
	// crashes. After EnableIRQs, IF=1 naturally, so timer preemption works.
	PUSHFQ
	POPQ	AX
	FST(R12, ThreadContext_RFLAGS, AX)

	// Save RSP (caller's RSP = our SP + 8 for the return address)
	LEAQ	8(SP), AX
	FST(R12, ThreadContext_RSP, AX)

	// Save FS_BASE MSR (per-thread TLS base)
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX
	FST(R12, ThreadContext_FSBase, AX)

	// Save CS/SS — kernel context
	MOVQ	$0x08, ThreadContext_CS(R12)	// CS = kernelCS
	MOVQ	$0x10, ThreadContext_SS(R12)	// SS = kernelSS

	// Save XMM directly into THIS thread's ctx.XMM (MAZ-139: no global staging).
	// The interrupted XMM is still live here — only integer GPR stores and the
	// FS_BASE RDMSR ran since entry, none of which touch XMM — so we store the
	// live registers straight into the per-thread context. load_context_and_iretq
	// restores them on reschedule.
	MOVOU	X0, ThreadContext_XMM+0(R12)
	MOVOU	X1, ThreadContext_XMM+16(R12)
	MOVOU	X2, ThreadContext_XMM+32(R12)
	MOVOU	X3, ThreadContext_XMM+48(R12)
	MOVOU	X4, ThreadContext_XMM+64(R12)
	MOVOU	X5, ThreadContext_XMM+80(R12)
	MOVOU	X6, ThreadContext_XMM+96(R12)
	MOVOU	X7, ThreadContext_XMM+112(R12)
	MOVOU	X8, ThreadContext_XMM+128(R12)
	MOVOU	X9, ThreadContext_XMM+144(R12)
	MOVOU	X10, ThreadContext_XMM+160(R12)
	MOVOU	X11, ThreadContext_XMM+176(R12)
	MOVOU	X12, ThreadContext_XMM+192(R12)
	MOVOU	X13, ThreadContext_XMM+208(R12)
	MOVOU	X14, ThreadContext_XMM+224(R12)
	MOVOU	X15, ThreadContext_XMM+240(R12)
	// FRAME-SAVE-END

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

	// FRAME-RESTORE-BEGIN yield-to-ready
	// Restore FS_BASE from context (per-thread TLS base).
	FLD(R12, ThreadContext_FSBase, AX)
	TESTQ	AX, AX
	JZ	yield_skip_tls		// FSBase==0 → skip WRMSR AND TLS sync
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR

	// Sync TLS: write g to FS_BASE - 8.
	// Skip if g==0 (thread not yet initialized): the demand page hasn't been
	// faulted in yet, and writing nil from supervisor mode would cause a
	// nested kernel page fault.
	// MAZ-135: source TLS-g from ctx.TLSG (the dual-home g), not ctx.R14, so this
	// path is faithful to the captured g like load_context_and_iretq and cannot
	// propagate a stale g0 into the TLS home → "morestack on g0".
	FLD(R12, ThreadContext_TLSG, DX)	// dual-home g (ctx.TLSG)
	TESTQ	DX, DX
	JZ	yield_skip_tls		// g==0 → skip TLS sync (page may not be present)
	FLD(R12, ThreadContext_FSBase, AX)	// FSBase (saved value — avoids WRMSR→RDMSR race)
	MOVQ	DX, -8(AX)		// Write g to TLS slot
yield_skip_tls:

	// Check for corrupted RIP
	FLD(R12, ThreadContext_RIP, AX)
	CMPQ	AX, $0x100000
	JAE	yr_rip_ok
	MOVW	$0x3F8, DX
	MOVB	$'!', AX; OUTB
	MOVB	$'Y', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_RIP, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
yr_halt:
	HLT
	JMP	yr_halt
yr_rip_ok:
	// MAZ-136 IRETQ guard: a kernel-CS context must resume inside kernel
	// .text (bounds zero = not yet published, skip; bad_ctx_dump in
	// exceptions_amd64.s prints from R12 and halts).
	MOVQ	·kernelTextHi(SB), AX
	TESTQ	AX, AX
	JZ	yr_guard_ok		// bounds not initialized yet
	CMPQ	ThreadContext_CS(R12), $0x08
	JNE	yr_guard_ok		// user context: any RIP is plausible
	FLD(R12, ThreadContext_RIP, R13)
	CMPQ	R13, AX			// RIP >= etext?
	JAE	yr_guard_bad
	MOVQ	·kernelTextLo(SB), AX
	CMPQ	R13, AX			// RIP >= text?
	JAE	yr_guard_ok
yr_guard_bad:
	JMP	bad_ctx_dump(SB)
yr_guard_ok:
	// MAZ-136 rotations: deliberately NO TSS.IST1 or TSS.RSP0 action on this
	// switch. Yield is not an exception (no chain dies → nothing to retire),
	// and both cursors are GLOBAL — they already protect the suspended chains
	// of every context, including a preempted kernel target's and parked
	// SyscallWaitSoftIRQ chains. Writing any per-context value here
	// re-creates the KVM-run-4 shared-stack trample. See the IST ROTATION
	// and RSP0 ROTATION banners in exceptions_amd64.s.
	// Restore XMM registers from target thread's ctx.XMM
	MOVOU	ThreadContext_XMM+0(R12), X0
	MOVOU	ThreadContext_XMM+16(R12), X1
	MOVOU	ThreadContext_XMM+32(R12), X2
	MOVOU	ThreadContext_XMM+48(R12), X3
	MOVOU	ThreadContext_XMM+64(R12), X4
	MOVOU	ThreadContext_XMM+80(R12), X5
	MOVOU	ThreadContext_XMM+96(R12), X6
	MOVOU	ThreadContext_XMM+112(R12), X7
	MOVOU	ThreadContext_XMM+128(R12), X8
	MOVOU	ThreadContext_XMM+144(R12), X9
	MOVOU	ThreadContext_XMM+160(R12), X10
	MOVOU	ThreadContext_XMM+176(R12), X11
	MOVOU	ThreadContext_XMM+192(R12), X12
	MOVOU	ThreadContext_XMM+208(R12), X13
	MOVOU	ThreadContext_XMM+224(R12), X14
	MOVOU	ThreadContext_XMM+240(R12), X15

	// Build IRETQ frame on current stack (same pattern as load_context_and_iretq)
	LEAQ	-40(SP), SP		// make room for 5 QWORDs
	FLD(R12, ThreadContext_SS, AX)
	MOVQ	AX, 32(SP)
	FLD(R12, ThreadContext_RSP, AX)
	MOVQ	AX, 24(SP)
	FLD(R12, ThreadContext_RFLAGS, AX)
	MOVQ	AX, 16(SP)
	FLD(R12, ThreadContext_CS, AX)
	MOVQ	AX, 8(SP)
	FLD(R12, ThreadContext_RIP, AX)
	MOVQ	AX, 0(SP)

	// Load all GPRs from context via the shared CTX_GPRS list; R12 (the base
	// pointer) is loaded last, outside the list.
	CTX_GPRS(FLD, R12)
	FLD(R12, ThreadContext_R12, R12)	// Load R12 last
	// FRAME-RESTORE-END

	IRETQ

yield_restore_return:
	// No thread to switch to — restore XMM from this thread's ctx and return
	// normally. R12 was overwritten by the SaveThread0AndYield return value
	// (0 here), so reload it. CurrentThread is unchanged on the no-switch path,
	// so it still points at the thread whose live XMM we saved into ctx above.
	MOVQ	·CurrentThread(SB), R12
	MOVQ	·ThreadContextOffset(SB), AX
	ADDQ	AX, R12			// R12 = &Thread.Context
	MOVOU	ThreadContext_XMM+0(R12), X0
	MOVOU	ThreadContext_XMM+16(R12), X1
	MOVOU	ThreadContext_XMM+32(R12), X2
	MOVOU	ThreadContext_XMM+48(R12), X3
	MOVOU	ThreadContext_XMM+64(R12), X4
	MOVOU	ThreadContext_XMM+80(R12), X5
	MOVOU	ThreadContext_XMM+96(R12), X6
	MOVOU	ThreadContext_XMM+112(R12), X7
	MOVOU	ThreadContext_XMM+128(R12), X8
	MOVOU	ThreadContext_XMM+144(R12), X9
	MOVOU	ThreadContext_XMM+160(R12), X10
	MOVOU	ThreadContext_XMM+176(R12), X11
	MOVOU	ThreadContext_XMM+192(R12), X12
	MOVOU	ThreadContext_XMM+208(R12), X13
	MOVOU	ThreadContext_XMM+224(R12), X14
	MOVOU	ThreadContext_XMM+240(R12), X15
	RET

yield_no_thread:
	RET
