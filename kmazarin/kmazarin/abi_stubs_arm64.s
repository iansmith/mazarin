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
	// Debug: print 'R' to show we entered RunFirstThread
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'R', R11
	MOVB	R11, (R10)

	// Call StartFirstThread to get context pointer
	// Uses Go ABI internally, returns result in R0
	SUB	$16, RSP  // Allocate space for call (16-byte aligned)
	CALL	·StartFirstThread(SB)
	MOVD	8(RSP), R20  // R20 = context pointer (returned in first return slot)
	ADD	$16, RSP  // Clean up call frame

	// Debug: print 'X' to show StartFirstThread returned
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'X', R11
	MOVB	R11, (R10)

	// Debug: print context pointer high bits
	MOVD	$'[', R11
	MOVB	R11, (R10)
	LSR	$60, R20, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	ctx_ptr_digit
	ADD	$('A'-10), R11
	B	ctx_ptr_print
ctx_ptr_digit:
	ADD	$'0', R11
ctx_ptr_print:
	MOVB	R11, (R10)
	MOVD	$']', R11
	MOVB	R11, (R10)

	// R20 = pointer to ThreadContext
	// Load ELR_EL1 (offset 256)
	MOVD	256(R20), R0
	MSR	R0, ELR_EL1

	// Debug: print 'E' to show ELR loaded
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'e', R11
	MOVB	R11, (R10)

	// Load SPSR_EL1 (offset 264)
	MOVD	264(R20), R0
	MSR	R0, SPSR_EL1

	// Switch to EL1h mode to safely set SP_EL0
	MOVD	$1, R0
	MSR	R0, SPSel  // SPSel=1 means use SP_EL1

	// Load SP_EL0 (offset 248)
	MOVD	248(R20), R0
	MSR	R0, SP_EL0

	// Debug: print SP value (8 hex digits = 32 bits) after loading
	// This distinguishes 0x7fff0000ffc0 (high 32 bits = 0x00007fff) from
	// 0x0000000000000030 (high 32 bits = 0x00000000)
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'S', R11
	MOVB	R11, (R10)
	MOVD	$'[', R11
	MOVB	R11, (R10)
	// R0 still has SP value - print 8 nibbles (high 32 bits)
	MOVD	R0, R14  // Save SP value
	MOVD	$8, R13  // Counter
sp_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	sp_digit
	ADD	$('A'-10), R11
	B	sp_print
sp_digit:
	ADD	$'0', R11
sp_print:
	MOVB	R11, (R10)
	LSL	$4, R14
	SUB	$1, R13
	CBNZ	R13, sp_loop
	MOVD	$']', R11
	MOVB	R11, (R10)

	// Debug output BEFORE loading GPRs (so we don't corrupt registers)
	// Print 'Z' and X28 value from context
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'Z', R11
	MOVB	R11, (R10)
	MOVD	$'[', R11
	MOVB	R11, (R10)
	MOVD	224(R20), R14  // X28 value from context (offset 28*8)
	LSR	$60, R14, R11  // High nibble
	AND	$0xF, R11
	CMP	$10, R11
	BLT	x28_digit1
	ADD	$('A'-10), R11
	B	x28_print1
x28_digit1:
	ADD	$'0', R11
x28_print1:
	MOVB	R11, (R10)
	LSR	$56, R14, R11  // Second nibble
	AND	$0xF, R11
	CMP	$10, R11
	BLT	x28_digit2
	ADD	$('A'-10), R11
	B	x28_print2
x28_digit2:
	ADD	$'0', R11
x28_print2:
	MOVB	R11, (R10)
	MOVD	$']', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)

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

	// DEBUG: Print ELR_EL1 and SPSR_EL1 right before loading GPRs
	// This shows exactly what we're about to ERET to
	MOVD	$0xFFFFFFFF09000000, R10
	MOVD	$'@', R11
	MOVB	R11, (R10)
	// Print ELR_EL1 (4 hex digits, low 16 bits)
	MRS	ELR_EL1, R14
	LSR	$12, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	rft_elr_d1
	ADD	$('A'-10), R11
	B	rft_elr_c1
rft_elr_d1:
	ADD	$'0', R11
rft_elr_c1:
	MOVB	R11, (R10)
	LSR	$8, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	rft_elr_d2
	ADD	$('A'-10), R11
	B	rft_elr_c2
rft_elr_d2:
	ADD	$'0', R11
rft_elr_c2:
	MOVB	R11, (R10)
	LSR	$4, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	rft_elr_d3
	ADD	$('A'-10), R11
	B	rft_elr_c3
rft_elr_d3:
	ADD	$'0', R11
rft_elr_c3:
	MOVB	R11, (R10)
	AND	$0xF, R14, R11
	CMP	$10, R11
	BLT	rft_elr_d4
	ADD	$('A'-10), R11
	B	rft_elr_c4
rft_elr_d4:
	ADD	$'0', R11
rft_elr_c4:
	MOVB	R11, (R10)
	// Print SPSR_EL1 (2 hex digits)
	MOVD	$'/', R11
	MOVB	R11, (R10)
	MRS	SPSR_EL1, R14
	LSR	$4, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	rft_spsr_d1
	ADD	$('A'-10), R11
	B	rft_spsr_c1
rft_spsr_d1:
	ADD	$'0', R11
rft_spsr_c1:
	MOVB	R11, (R10)
	AND	$0xF, R14, R11
	CMP	$10, R11
	BLT	rft_spsr_d2
	ADD	$('A'-10), R11
	B	rft_spsr_c2
rft_spsr_d2:
	ADD	$'0', R11
rft_spsr_c2:
	MOVB	R11, (R10)
	MOVD	$'@', R11
	MOVB	R11, (R10)
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
