// abi_stubs_arm64.s - ABI0 entry points for functions called from assembly
//
// When assembly in this package calls main·functionName, Go expects ABI0 entry
// points. These stubs provide ABI0 wrappers that read arguments from the stack
// and call the ABIInternal implementations with arguments in registers.

#include "textflag.h"

// SyscallDispatch is called from exceptions_arm64.s
// Go signature: func SyscallDispatch(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64
// ABI0: 7 args (56 bytes) + 1 return (8 bytes) = 64 bytes
//
// Tail-call to the internal function. The .abi0 wrapper will read args from
// our caller's stack (which is exactly where they were placed by exceptions_arm64.s).
// This avoids adding to the nosplit stack chain.
TEXT ·SyscallDispatch(SB), NOSPLIT, $0-64
	JMP	·syscallDispatchInternal(SB)

// IRQDispatch is called from exceptions_arm64.s
// Go signature: func IRQDispatch(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool)
// ABI0: 4 args (32 bytes) + 4 returns (32 bytes) = 64 bytes
//
// Tail-call to internal function. The internal's .abi0 wrapper reads from our stack.
TEXT ·IRQDispatch(SB), NOSPLIT, $0-64
	JMP	·irqDispatchInternal(SB)

// TimerIRQHandler is called from exceptions_arm64.s for timer IRQs
// Go signature: func TimerIRQHandler(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool)
// ABI0: 4 args (32 bytes) + 4 returns (32 bytes) = 64 bytes
TEXT ·TimerIRQHandler(SB), NOSPLIT, $0-64
	JMP	·timerIRQHandlerInternal(SB)

// HandlePageFaultAsm is called from data_abort in exceptions_arm64.s
// Go signature: func HandlePageFaultAsm(faultAddr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
// Returns 1 if handled, 0 if not.
//
// ARM64 does not need NOSPLIT here: SP_EL1 (exception stack, 16KB) is above
// g0.stackguard0, so the stack check passes. NOSPLIT would exceed the 792-byte
// nosplit chain limit due to ExceptionVectorTable's 352-byte frame.
TEXT ·HandlePageFaultAsm(SB), $0-16
	JMP	·handlePageFaultInternal(SB)

// HandleUserPageFaultAsm is called from el0_sync_handler for data aborts from EL0
// Go signature: func HandleUserPageFaultAsm(faultAddr, isPermFault uint64) uint64
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
// Returns 1 if handled, 0 if not.
TEXT ·HandleUserPageFaultAsm(SB), $0-24
	JMP	·handleUserPageFaultInternal(SB)

// GetSyscallSwitchTarget returns context switch target set by syscall handlers
// Go signature: func GetSyscallSwitchTarget() uint64
// ABI0: 0 args + 1 return (8 bytes) = 8 bytes
// Returns thread node pointer as uint64, 0 = no switch needed
//
// CRITICAL: Cannot use JMP tail-call for functions with return values!
// The .abi0 wrapper writes returns relative to its entry SP, but our caller
// reads from a different offset. Must use CALL and copy return value.
TEXT ·GetSyscallSwitchTarget(SB), NOSPLIT, $16-8
	// Frame: 16 bytes local (for internal's return) + 8 bytes for our return
	// Call internal - it will write return to 8(SP) relative to its entry
	CALL	·getSyscallSwitchTargetInternal(SB)
	// Internal wrote return to 8(SP) where SP is our frame
	// Load it and store to our return slot
	MOVD	8(RSP), R0
	MOVD	R0, ret+0(FP)
	RET

// DoContextSwitch saves current context and returns new context pointer
// Go signature: func DoContextSwitch(framePtr uint64, targetPtr uint64) uint64
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
// targetPtr is thread node pointer (not index)
//
// CRITICAL: Cannot use JMP tail-call for functions with return values!
TEXT ·DoContextSwitch(SB), NOSPLIT, $32-24
	// Load args from our caller's frame
	MOVD	framePtr+0(FP), R0
	MOVD	targetPtr+8(FP), R1
	// Store to our local frame for internal call
	MOVD	R0, 8(RSP)
	MOVD	R1, 16(RSP)
	// Call internal
	CALL	·doContextSwitchABI0(SB)
	// Load return from internal's return slot and store to ours
	MOVD	24(RSP), R0
	MOVD	R0, ret+16(FP)
	RET

// SetSyscallELR stores the ELR for current syscall
// Go signature: func SetSyscallELR(elr uint64)
// ABI0: 1 arg (8 bytes) + 0 return = 8 bytes
TEXT ·SetSyscallELR(SB), NOSPLIT, $0-8
	JMP	·setSyscallELRInternal(SB)

// SetSyscallSPSR stores the SPSR for current syscall
// Go signature: func SetSyscallSPSR(spsr uint64)
// ABI0: 1 arg (8 bytes) + 0 return = 8 bytes
TEXT ·SetSyscallSPSR(SB), NOSPLIT, $0-8
	JMP	·setSyscallSPSRInternal(SB)

// SetSyscallCloneRegs is a no-op on ARM64 (clone stores on child stack).
TEXT ·SetSyscallCloneRegs(SB), NOSPLIT, $0-24
	RET

// CheckThreadPreemption checks if thread preemption is needed and performs switch
// Go signature: func CheckThreadPreemption(framePtr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
// Returns pointer to new ThreadContext if switch happened, 0 otherwise
TEXT ·CheckThreadPreemption(SB), NOSPLIT, $0-16
	JMP	·checkThreadPreemptionInternal(SB)

// CheckKernelGoroutinePreempt checks kernel goroutine async preemption.
// Go signature: func CheckKernelGoroutinePreempt(framePtr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
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
// NOT NOSPLIT: Same as HandlePageFaultAsm — exception stack is above g0.stackguard0.
TEXT ·HandleUnhandledExceptionAsm(SB), $0-32
	JMP	·handleUnhandledExceptionInternal(SB)

// ============================================================================
// YieldToReadyThread - Save thread 0 context and ERET to next ready thread
// ============================================================================
// Saves ALL registers of the current thread (thread 0) into its ThreadContext,
// puts it on the ready queue, then ERETs to the next ready thread.
// When thread 0 is scheduled back via timer preemption, execution resumes
// at the instruction after the call to YieldToReadyThread.
//
// If no other thread is ready, returns without yielding.
//
// ThreadContext layout:
//   X[0..30]  offsets 0..240   (31 * 8 = 248 bytes)
//   SP        offset 248
//   ELR       offset 256
//   SPSR      offset 264
//
TEXT ·YieldToReadyThread(SB), NOSPLIT|NOFRAME, $0-0
	// Check CurrentThread using R0 (caller-saved) to preserve R20 (callee-saved).
	// This matters when called directly (JMP from kmazarinYieldImpl) rather than
	// via SVC exception handler — we must preserve callee-saved regs for the
	// no-switch return path (yield_restore_return).
	MOVD	·CurrentThread(SB), R0
	CBZ	R0, yield_no_thread

	// Compute context pointer: R0 = &Thread.Context
	MOVD	·ThreadContextOffset(SB), R1
	ADD	R1, R0

	// Save original R20 BEFORE clobbering it
	MOVD	R20, 160(R0)  // ThreadContext offset 160 = X20

	// Now use R20 as context pointer
	MOVD	R0, R20

	// Save callee-saved registers and key registers into ThreadContext.
	// R0 and R1 are already clobbered (context ptr, offset) — they're caller-saved
	// so wrong values don't affect the return path. The ERET path loads from the
	// NEW thread's context, so stale R0/R1 in THIS thread's context is harmless.

	// Save X0-X27 (offsets 0-216)
	STP	(R0, R1), 0(R20)
	STP	(R2, R3), 16(R20)
	STP	(R4, R5), 32(R20)
	STP	(R6, R7), 48(R20)
	STP	(R8, R9), 64(R20)
	STP	(R10, R11), 80(R20)
	STP	(R12, R13), 96(R20)
	STP	(R14, R15), 112(R20)
	STP	(R16, R17), 128(R20)
	// X18 (platform register) - can't use R18 in Go asm, encode directly
	WORD	$0xF9004A92  // STR X18, [X20, #144]  (offset 18*8=144)
	// X19 - save individually
	MOVD	R19, 152(R20)
	// X20 - already saved at offset 160 above (original value, before clobbering)
	// X21-X27
	MOVD	R21, 168(R20)
	STP	(R22, R23), 176(R20)
	STP	(R24, R25), 192(R20)
	STP	(R26, R27), 208(R20)

	// Save X28 (g register) - can't use R28 in Go asm, use MRS/encode
	// x28 is the g pointer; we need to read it
	WORD	$0xF900729C  // STR X28, [X20, #224]  (offset 28*8=224)

	// Save X29 (FP) and X30 (LR)
	STP	(R29, R30), 232(R20)

	// [MAZ-193] Capture the REAL processor state. This block previously
	// assumed the caller always runs at EL1t and hardcoded both values:
	//   MOVD RSP, R0        // "current SP = SP_EL0 since we're in EL1t"
	//   MOVD $0x4, R0       // "EL1t mode, all exceptions unmasked"
	// Both are false when the caller yields from HANDLER context (EL1h) —
	// which pushStringFull does on console backpressure (xfertest's
	// [constest] stage forces exactly this). The hardcode then wrote an
	// exception-stack address into the thread's SP and mislabelled the
	// mode as EL1t with all exceptions unmasked; the later ERET resumed
	// handler code at EL1t/DAIF=0 on an SP_EL0 alias of the exception
	// stack (fatal signature: E:00 EL=1), and force-unmasked DAIF on
	// every legitimate thread-0 yield-resume (MAZ-192).
	//
	// SPSel selects which SP is live: 0 = SP_EL0 (EL1t), 1 = SP_EL1 (EL1h).
	MRS	SPSel, R1
	CBZ	R1, yield_sp_el0_is_rsp
	// EL1h: RSP is SP_EL1 (the shared exception stack), NOT this thread's
	// stack. Save SP_EL0 instead — but NOTE: for an EL0-originated
	// exception (the common case) SP_EL0 still holds the USERSPACE SP at
	// this point (exception entry saves/restores it in the trap frame but
	// never repoints it), so even this "truthful" save does NOT produce a
	// resumable context — SP_EL1 is one shared stack and is never restored
	// per-thread. EL1h callers must not park: pushStringFull refuses at
	// the call site (drops instead); a self-enforcing guard here is
	// MAZ-196. This branch exists so a future violation saves honest
	// state for the post-mortem rather than a mislabelled EL1t context.
	MRS	SP_EL0, R0
	B	yield_sp_saved
yield_sp_el0_is_rsp:
	MOVD	RSP, R0
yield_sp_saved:
	MOVD	R0, 248(R20)

	// Save ELR = LR (return address — where to resume when scheduled back)
	MOVD	R30, 256(R20)

	// SPSR = real DAIF (bits 9:6 — identical layout in DAIF and SPSR_EL1)
	// OR the real mode M[3:0]: 4 = EL1t, 5 = EL1h. R1 still holds SPSel.
	MRS	DAIF, R0
	ADD	$4, R1, R1
	ORR	R1, R0, R0
	MOVD	R0, 264(R20)

	// Save LR in R19 before CALL clobbers it (R30).
	// Original R19 is already saved to ThreadContext at offset 152 above.
	MOVD	R30, R19

	// Call SaveThread0AndYield() which returns context pointer in R0
	// func SaveThread0AndYield() uint64
	SUB	$16, RSP
	CALL	·SaveThread0AndYield(SB)
	MOVD	8(RSP), R20  // R20 = context pointer (first return value)
	ADD	$16, RSP

	// Restore LR from R19 (CALL clobbered R30)
	MOVD	R19, R30

	// Check if we got a thread to switch to
	CBZ	R20, yield_restore_return

	// ========================================================
	// ERET to new thread. Restore convention: ELR/SPSR/SP_EL0 from the
	// ThreadContext first, then all GPRs — X28 (g) first via R0 as temp,
	// R0/R1 reloaded after, and the context base register (R20) last.
	// ========================================================

	// Load ELR_EL1 (offset 256)
	MOVD	256(R20), R0
	MSR	R0, ELR_EL1

	// Load SPSR_EL1 (offset 264)
	MOVD	264(R20), R0
	MSR	R0, SPSR_EL1

	// Switch to EL1h mode to safely set SP_EL0
	MOVD	$1, R0
	MSR	R0, SPSel  // SPSel=1 means use SP_EL1

	// Load SP_EL0 (offset 248)
	MOVD	248(R20), R0
	MSR	R0, SP_EL0

	// I-cache and TLB invalidation
	WORD	$0xD508751F  // IC IALLU
	DSB	$15
	WORD	$0xD508871F  // TLBI VMALLE1
	DSB	$15
	ISB	$15

	// Load all GPRs from new thread's ThreadContext
	// X28 (g register) first using R0 as temp
	MOVD	224(R20), R0
	WORD	$0xAA0003FC  // MOV X28, X0

	// Load other GPRs
	LDP	16(R20), (R2, R3)
	LDP	32(R20), (R4, R5)
	LDP	48(R20), (R6, R7)
	LDP	64(R20), (R8, R9)
	LDP	80(R20), (R10, R11)
	LDP	96(R20), (R12, R13)
	LDP	112(R20), (R14, R15)
	LDP	128(R20), (R16, R17)
	MOVD	152(R20), R19
	MOVD	168(R20), R21
	LDP	176(R20), (R22, R23)
	LDP	192(R20), (R24, R25)
	LDP	208(R20), (R26, R27)
	LDP	232(R20), (R29, R30)

	// Reload R0, R1 (corrupted by X28 load)
	LDP	0(R20), (R0, R1)

	// Load R20 LAST
	MOVD	160(R20), R20

	// Barriers and ERET
	DSB	$15
	ISB	$15
	ERET
	DSB	$15
	ISB	$15

yield_restore_return:
	// No thread to switch to — return normally.
	// Thread 0's context was saved but SaveThread0AndYield undid the queue change.
	// R30 (LR) was already restored from R19 above.
	// R19 is clobbered (was used to temp-save LR) — restore from ThreadContext.
	// R20 is clobbered (has return value 0) — restore from ThreadContext.
	MOVD	·CurrentThread(SB), R0
	MOVD	·ThreadContextOffset(SB), R1
	ADD	R1, R0
	MOVD	152(R0), R19  // restore original R19
	MOVD	160(R0), R20  // restore original R20
	RET

yield_no_thread:
	// CurrentThread is nil — nothing to do
	RET

// readELR_EL1 returns ELR_EL1 — the interrupted PC. When called early in an
// exception/IRQ handler (before ELR is clobbered or ERET runs), this is the PC
// of the code that was running when the exception was taken. Used by the
// [SCHEDLK-VIOLATION] detector to identify who held schedulerLock with IRQs
// enabled when the timer fired.
//
// func readELR_EL1() uint64
TEXT ·readELR_EL1(SB), NOSPLIT|NOFRAME, $0-8
	MRS	ELR_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// inHandlerContextASM returns SPSel: nonzero = running on SP_EL1, the
// shared exception stack (EL1h handler context). pushStringFull uses it
// to refuse to park from handler context (MAZ-193) — a suspended
// mid-handler continuation lives on the shared exception stack, which is
// never restored per-thread, so it cannot be legally resumed.
//
// func inHandlerContextASM() uint64
TEXT ·inHandlerContextASM(SB), NOSPLIT|NOFRAME, $0-8
	MRS	SPSel, R0
	MOVD	R0, ret+0(FP)
	RET
