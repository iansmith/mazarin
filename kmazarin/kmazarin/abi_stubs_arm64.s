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
// Note: Not using NOSPLIT because handlePageFaultInternal needs full call chain.
// We're running on SP_EL1 (exception stack, 16KB) which has plenty of room.
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

// CheckThreadPreemption checks if thread preemption is needed and performs switch
// Go signature: func CheckThreadPreemption(framePtr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
// Returns pointer to new ThreadContext if switch happened, 0 otherwise
TEXT ·CheckThreadPreemption(SB), NOSPLIT, $0-16
	JMP	·checkThreadPreemptionInternal(SB)

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

	// Load all general purpose registers from ThreadContext
	// X[0-30] at offsets 0-240
	LDP	0(R20), (R0, R1)
	LDP	16(R20), (R2, R3)
	LDP	32(R20), (R4, R5)
	LDP	48(R20), (R6, R7)
	LDP	64(R20), (R8, R9)
	LDP	80(R20), (R10, R11)
	LDP	96(R20), (R12, R13)
	LDP	112(R20), (R14, R15)
	LDP	128(R20), (R16, R17)
	// Skip R18 (platform register)
	LDP	152(R20), (R19, R21)  // R19 and R21 (skip R20 which we're using)
	LDP	176(R20), (R22, R23)
	LDP	192(R20), (R24, R25)
	LDP	208(R20), (R26, R27)
	// Load X28 (g register)
	MOVD	224(R20), R0  // Temporarily use R0
	WORD	$0xAA0003FC  // MOV X28, X0 (can't use R28 directly)
	// Load X29 (FP) and X30 (LR)
	LDP	232(R20), (R29, R30)

	// Now load the R20 value from context (we were using it as pointer)
	// Load it last since we were using R20 as our context pointer
	MOVD	160(R20), R20  // Finally load the actual X20 value

	// TLB invalidation before ERET (following Linux kernel pattern)
	WORD	$0xD508871F  // TLBI VMALLE1
	DSB	$15  // DSB SY
	WORD	$0xD508877F  // TLBI VALE1, XZR
	DSB	$11  // DSB NSH
	ISB	$15  // ISB

	// Transition to userspace
	ERET

	// Speculation barrier after ERET
	DSB	$15
	ISB	$15

	// Should never reach here
run_first_thread_hang:
	B	run_first_thread_hang
