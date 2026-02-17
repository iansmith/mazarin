//go:build !test_stubs

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
// Go signature: func HandleUserPageFaultAsm(faultAddr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
// Returns 1 if handled, 0 if not.
TEXT ·HandleUserPageFaultAsm(SB), $0-16
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

// ThreadExitAsm tail-call stub
// Called from exception handler to kill faulting user thread.
// Returns pointer to next ThreadContext (0 if no threads left).
TEXT ·ThreadExitAsm(SB), NOSPLIT, $0-8
	JMP	·threadExitInternal(SB)

// RunFirstThread starts the first thread from the ready queue.
// This function never returns - it transitions to userspace via ERET.
// Go signature: func RunFirstThread()
// ThreadContext layout: X[31]*8=248 bytes, SP(8), ELR(8), SPSR(8) = 272 bytes
TEXT ·RunFirstThread(SB), NOSPLIT|NOFRAME, $0-0
	// Call StartFirstThread to get context pointer
	// Uses Go ABI internally, returns result in R0
	SUB	$16, RSP  // Allocate space for call (16-byte aligned)
	CALL	·StartFirstThread(SB)
	MOVD	8(RSP), R20  // R20 = context pointer (returned in first return slot)
	ADD	$16, RSP  // Clean up call frame

	// R20 = pointer to ThreadContext
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

	// I-cache and TLB invalidation BEFORE loading GPRs
	// CRITICAL: Invalidate entire I-cache to ensure no stale instruction fetch
	// IC IALLU = 0xD508751F (Invalidate All to PoU, Inner Shareable)
	WORD	$0xD508751F  // IC IALLU
	DSB	$15  // DSB SY - ensure I-cache invalidation completes
	// Now invalidate TLB
	WORD	$0xD508871F  // TLBI VMALLE1
	DSB	$15  // DSB SY
	WORD	$0xD508877F  // TLBI VALE1, XZR
	DSB	$11  // DSB NSH
	ISB	$15  // ISB

	// DEBUG: Print TTBR0 value before ERET (using R0-R3 as scratch, will reload later)
	MOVD	$0xFFFFFFFF09000000, R2  // UART base (kernel-mapped VA)
	MOVD	$'T', R3
	MOVB	R3, (R2)
	MOVD	$'0', R3
	MOVB	R3, (R2)
	MOVD	$'=', R3
	MOVB	R3, (R2)
	// Read TTBR0_EL1
	MRS	TTBR0_EL1, R1
	MOVD	$16, R3  // 16 hex digits
rft_print_ttbr0:
	LSR	$60, R1, R0
	AND	$0xF, R0
	CMP	$10, R0
	BLT	rft_ttbr0_d
	ADD	$('A'-10), R0
	B	rft_ttbr0_c
rft_ttbr0_d:
	ADD	$'0', R0
rft_ttbr0_c:
	MOVB	R0, (R2)
	LSL	$4, R1
	SUB	$1, R3
	CBNZ	R3, rft_print_ttbr0
	// Print ELR
	MOVD	$' ', R3
	MOVB	R3, (R2)
	MOVD	$'E', R3
	MOVB	R3, (R2)
	MOVD	$'=', R3
	MOVB	R3, (R2)
	MRS	ELR_EL1, R1
	MOVD	$16, R3
rft_print_elr:
	LSR	$60, R1, R0
	AND	$0xF, R0
	CMP	$10, R0
	BLT	rft_elr_d
	ADD	$('A'-10), R0
	B	rft_elr_c
rft_elr_d:
	ADD	$'0', R0
rft_elr_c:
	MOVB	R0, (R2)
	LSL	$4, R1
	SUB	$1, R3
	CBNZ	R3, rft_print_elr
	MOVD	$'\r', R3
	MOVB	R3, (R2)
	MOVD	$'\n', R3
	MOVB	R3, (R2)
	// END DEBUG

	// Load all general purpose registers from ThreadContext
	// X[0-30] at offsets 0-240
	// IMPORTANT: Load X28 first using R0 as temp, then reload R0 at the end

	// Load X28 (g register) using R0 as temporary
	MOVD	224(R20), R0
	WORD	$0xAA0003FC  // MOV X28, X0 (can't use R28 directly in Go asm)

	// Load other GPRs (skip R0, R1 for now - will reload after X28 transfer)
	LDP	16(R20), (R2, R3)
	LDP	32(R20), (R4, R5)
	LDP	48(R20), (R6, R7)
	LDP	64(R20), (R8, R9)
	LDP	80(R20), (R10, R11)
	LDP	96(R20), (R12, R13)
	LDP	112(R20), (R14, R15)
	LDP	128(R20), (R16, R17)
	// Skip R18 (platform register) at offset 144
	// Skip R20 (at offset 160, loaded last since it's our context pointer)
	// Load X[19] individually from offset 152
	MOVD	152(R20), R19
	// Load X[21] individually from offset 168
	MOVD	168(R20), R21
	LDP	176(R20), (R22, R23)
	LDP	192(R20), (R24, R25)
	LDP	208(R20), (R26, R27)
	// Load X29 (FP) and X30 (LR)
	LDP	232(R20), (R29, R30)

	// CRITICAL: Reload R0 and R1 (R0 was corrupted when used as X28 temp)
	// Must do this while R20 still points to context
	LDP	0(R20), (R0, R1)

	// Load R20 LAST (we were using it as context pointer)
	MOVD	160(R20), R20

	// Synchronization barriers before ERET
	DSB	$15  // Data Synchronization Barrier - ensure all memory ops complete
	ISB	$15  // Instruction Synchronization Barrier - flush pipeline

	// Transition to userspace - NO DEBUG OUTPUT HERE (would corrupt registers)
	ERET

	// Speculation barrier after ERET
	DSB	$15
	ISB	$15

	// Should never reach here
run_first_thread_hang:
	B	run_first_thread_hang

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
	// R20 = pointer to current thread's ThreadContext
	// CurrentThread is an unsafe.Pointer to *Thread
	// Context is NOT the first field — add ThreadContextOffset
	MOVD	·CurrentThread(SB), R20
	CBZ	R20, yield_no_thread
	MOVD	·ThreadContextOffset(SB), R0
	ADD	R0, R20  // R20 now points to Thread.Context

	// Save callee-saved registers and key registers into ThreadContext.
	// We only need to save what the Go ABI + ERET restore path expects.
	// Caller-saved registers (R0-R15) don't need saving for the Go call
	// convention, but the ERET restore path loads ALL registers from
	// ThreadContext, so we save everything for correctness.

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
	// X20 is our context pointer - save its original value (0 for now, doesn't matter)
	// We'll fix this below
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

	// Save X20 itself (our context pointer) - it was the original R20 on entry.
	// The caller's R20 is callee-saved, but we clobbered it. However, on entry
	// R20 had whatever the Go caller had. Since we loaded CurrentThread into R20,
	// the original R20 is lost. Save 0 as placeholder — when resumed, R20 will
	// be restored from this, but Go will immediately set it from the call frame.
	MOVD	$0, R0
	MOVD	R0, 160(R20)

	// Save SP (current stack pointer = SP_EL0 since we're in EL1t)
	MOVD	RSP, R0
	MOVD	R0, 248(R20)

	// Save ELR = LR (return address — where to resume when scheduled back)
	MOVD	R30, 256(R20)

	// Save SPSR = 0x4 (EL1t mode, all exceptions unmasked)
	// When thread 0 is scheduled back, ERET restores this SPSR, returning to EL1t
	MOVD	$0x4, R0
	MOVD	R0, 264(R20)

	// Call SaveThread0AndYield() which returns context pointer in R0
	// func SaveThread0AndYield() uint64
	SUB	$16, RSP
	CALL	·SaveThread0AndYield(SB)
	MOVD	8(RSP), R20  // R20 = context pointer (first return value)
	ADD	$16, RSP

	// Check if we got a thread to switch to
	CBZ	R20, yield_restore_return

	// ========================================================
	// ERET to new thread (same pattern as RunFirstThread)
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
	// No thread to switch to — return normally
	// Thread 0's context was saved but SaveThread0AndYield undid the queue change.
	// Just return to caller.
	RET

yield_no_thread:
	// CurrentThread is nil — nothing to do
	RET
