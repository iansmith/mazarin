//go:build !test_stubs


// exceptions_arm64.s - Kmazarin exception handlers (Go/Plan9 assembly)
//
// This file provides the exception vector table and handlers for kmazarin.
// Cardinal sets VBAR_EL1 to point to this vector before jumping to kmazarin.
//
// CRITICAL: Exception vector MUST be 2KB aligned (ARM64 requirement)

#include "textflag.h"
#include "go_abi_macros_arm64.h"

// UART base for minimal debug output
// NOTE: Use high-memory UART address since kmazarin runs at high memory
// Cardinal maps UART at 0xFFFFFFFF09000000 before jumping to us
#define UART_BASE 0xFFFFFFFF09000000

// Exception frame layout (same as Cardinal for compatibility)
#define EXC_FRAME_X0         0
#define EXC_FRAME_X8         64
#define EXC_FRAME_X28        224
#define EXC_FRAME_X29_X30    232
#define EXC_FRAME_ELR_SPSR   256
#define EXC_FRAME_FAR_ESR    272
#define EXC_FRAME_SP_EL0     288
#define EXC_FRAME_SIZE       320

// ============================================================================
// EXCEPTION VECTOR TABLE
// ============================================================================
// ARM64 exception vector table - 16 entries, each 128 bytes (0x80)
// CRITICAL: Must be 2KB (0x800) aligned - enforced by .align 11
//
// We only implement "Current EL with SPx" entries (0x200-0x380)
// since kmazarin runs at EL1h mode (using SP_EL1)

// CRITICAL: Exception vector must be 2KB aligned
// Use PCALIGN $2048 to force 2KB (0x800) alignment
TEXT ·ExceptionVectorTable(SB), NOSPLIT|NOFRAME, $0
	PCALIGN $2048		// Force 2KB alignment
exception_vector_base:

// ============================================================================
// Current EL with SP0 (0x000-0x1FF) - Go runtime uses SP_EL0 at EL1
// ============================================================================
// Each vector entry must be 128 bytes - pad with NOPs
el1_sp0_sync:
	// Go runtime runs at EL1 using SP_EL0, so sync exceptions come here
	// CRITICAL: Do NOT use any registers here before saving them!
	// Go's clone relies on R10-R12 containing mp, gp, fn during page faults.
	B	sync_exception_handler
	// 1 instruction = 4 bytes, need 124 bytes padding = 31 WORDs
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_irq:
	// IRQs also come here when using SP_EL0
	// CRITICAL: Do NOT use any registers here before saving them!
	B	irq_exception_handler
	// 1 instruction = 4 bytes, need 124 bytes padding = 31 WORDs
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_fiq:
	// FIQ - unlikely, but save registers first
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_sp0_serror:
	// SError - unlikely, but save registers first
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	// CRITICAL: el1_spx_sync MUST start at EXACTLY offset 0x200 (no alignment!)
el1_spx_sync:
	B	sync_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_irq:
	B	irq_exception_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_fiq:
	// MOVD $UART_BASE generates 2 instructions (mov+movk), so 5 total = 20 bytes
	// Need 128 - 20 = 108 bytes = 27 WORDs of padding
	MOVD	$UART_BASE, R10
	MOVD	$'F', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0

el1_spx_serror:
	// MOVD $UART_BASE generates 2 instructions (mov+movk), so 5 total = 20 bytes
	// Need 128 - 20 = 108 bytes = 27 WORDs of padding
	MOVD	$UART_BASE, R10
	MOVD	$'S', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0

// ============================================================================
// Lower EL AArch64 (0x400-0x5FF) - Userspace exceptions (EL0)
// ============================================================================
el0_aarch64_sync:
	// Synchronous exception from EL0 (userspace)
	// This handles SVC syscalls from userspace
	B	el0_sync_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_irq:
	// IRQ from EL0 - for now, just handle like EL1 IRQ
	B	el0_irq_handler
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_fiq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_serror:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// Lower EL AArch32 (0x600-0x7FF) - Not supported
// ============================================================================
el0_aarch32_sync:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_irq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_fiq:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch32_serror:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// EXCEPTION HANDLERS
// ============================================================================

// Unhandled exception - print 'X' with ESR info and halt
unhandled_exception:
	MOVD	$UART_BASE, R10
	MOVD	$'X', R11
	MOVB	R11, (R10)
	MOVD	$':', R11
	MOVB	R11, (R10)

	// Print ESR_EL1 to identify exception type
	MRS	ESR_EL1, R12
	LSR	$26, R12, R12  // Extract EC (bits 31:26)
	AND	$0x3F, R12

	// Print EC as 2 hex digits
	LSR	$4, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_first_digit
	ADD	$('A'-10), R11
	B	print_first
print_first_digit:
	ADD	$'0', R11
print_first:
	MOVB	R11, (R10)

	AND	$0xF, R12
	CMP	$10, R12
	BLT	print_second_digit
	ADD	$('A'-10), R12
	B	print_second
print_second_digit:
	ADD	$'0', R12
print_second:
	MOVB	R12, (R10)

	MOVD	$'\r', R11
	MOVB	R11, (R10)
	MOVD	$'\n', R11
	MOVB	R11, (R10)
hang:
	B	hang

// ============================================================================
// sync_exception_handler - Synchronous exceptions (SVC, data abort, etc.)
// ============================================================================
sync_exception_handler:
	// CRITICAL: Switch to SP_EL1 IMMEDIATELY before using RSP for anything.
	// When an exception is taken from EL1t mode (Go code using SP_EL0),
	// ARM64 preserves PSTATE.SP=0, meaning RSP still aliases SP_EL0!
	// Without this switch, we'd corrupt the interrupted code's stack.
	MSR	$1, SPSel

	// CRITICAL: Save X0-X7 BEFORE any debug output!
	// These contain syscall arguments and must not be clobbered.
	// Allocate frame first
	SUB	$EXC_FRAME_SIZE, RSP

	// Save X0-X7 IMMEDIATELY (before debug print clobbers them!)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	STP	(R2, R3), EXC_FRAME_X0+16(RSP)
	STP	(R4, R5), EXC_FRAME_X0+32(RSP)
	STP	(R6, R7), EXC_FRAME_X0+48(RSP)

	// Save X8-X27
	STP	(R8, R9), EXC_FRAME_X8(RSP)
	STP	(R10, R11), EXC_FRAME_X8+16(RSP)
	STP	(R12, R13), EXC_FRAME_X8+32(RSP)
	STP	(R14, R15), EXC_FRAME_X8+48(RSP)
	STP	(R16, R17), EXC_FRAME_X8+64(RSP)
	// R18 is platform register, use raw instruction: str x18, [sp, #144]
	WORD	$0xf9004bf2  // str x18, [sp, #144]
	// Save R19: str x19, [sp, #152]
	WORD	$0xf9004ff3  // str x19, [sp, #152]
	STP	(R20, R21), EXC_FRAME_X8+96(RSP)
	STP	(R22, R23), EXC_FRAME_X8+112(RSP)
	STP	(R24, R25), EXC_FRAME_X8+128(RSP)
	STP	(R26, R27), EXC_FRAME_X8+144(RSP)

	// Save X28-X30 (R28 is g, use raw instruction)
	// str x28, [sp, #224]
	WORD	$0xf90073fc  // str x28, [sp, #224]
	// str x29, [sp, #232]
	WORD	$0xf90077fd  // str x29, [sp, #232]
	MOVD	LR, R10
	MOVD	R10, EXC_FRAME_X28+16(RSP)

	// Save ELR, SPSR, FAR, ESR
	MRS	ELR_EL1, R10
	MRS	SPSR_EL1, R11
	STP	(R10, R11), EXC_FRAME_ELR_SPSR(RSP)

	MRS	FAR_EL1, R10
	MRS	ESR_EL1, R11
	STP	(R10, R11), EXC_FRAME_FAR_ESR(RSP)

	// Save SP_EL0
	MRS	SP_EL0, R10
	MOVD	R10, EXC_FRAME_SP_EL0(RSP)

	// Extract exception class (EC) from ESR_EL1 (saved in exception frame)
	// EC is in bits 31:26
	MOVD	EXC_FRAME_FAR_ESR+8(RSP), R10  // Load ESR from frame
	LSR	$26, R10, R10
	AND	$0x3F, R10

	// Check if this is SVC (EC = 0x15)
	CMP	$0x15, R10
	BNE	not_svc

	// Mark that we're inside an SVC handler (unsafe to preempt by timer).
	// Must be set before any Go code runs. Cleared in sync_return before ERET.
	MOVD	$1, R10
	MOVW	R10, ·svcDepth(SB)

	// CRITICAL: Switch to kmazarin's g0 before calling any Go code!
	// The interrupted goroutine may have m.p=nil (parked without P), which
	// would crash if syscall handlers try to allocate. Using g0 ensures
	// we always have a valid mcache for heap operations.
	// Note: x28 was already saved to the exception frame, so we can safely modify it.
	// Only switch if kmazarinG0Addr is non-zero (initialized).
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, skip_g_switch_el1  // Skip if not initialized
	WORD	$0xaa0a03fc  // mov x28, x10
skip_g_switch_el1:

	// SVC: First save ELR and SPSR so clone can get child's return address and state
	// ELR and SPSR are already in the exception frame from earlier save
	// CRITICAL: R0-R17 are caller-saved and will be clobbered by function calls!
	// We must save SPSR to a callee-saved register (R19) before calling SetSyscallELR.
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R0, R19)  // Load ELR into R0, SPSR into R19 (callee-saved)
	GO_CALL_1_0(·SetSyscallELR, R0)         // SetSyscallELR(elr) - may clobber R0-R17
	GO_CALL_1_0(·SetSyscallSPSR, R19)       // SetSyscallSPSR(spsr) - R19 preserved

	// Clear InCloneSetup flag for current thread (if set)
	// This marks the clone child as having completed its setup phase.
	// After this, async preempt is safe because fn/gp/mp have been read from stack.
	MOVD	main·CurrentThread(SB), R10
	CBZ	R10, el1_skip_clear_clone_setup
	MOVD	main·ThreadInCloneSetupOffset(SB), R11
	ADD	R11, R10, R11
	MOVW	$0, R12
	MOVW	R12, (R11)  // thread.InCloneSetup = 0
el1_skip_clear_clone_setup:

	// Now call syscall dispatcher using ABI0 calling convention
	// Load arguments from exception frame first (before adjusting RSP)
	LDP	EXC_FRAME_X8(RSP), (R0, R1)        // R0 = syscall num (X8), R1 = X9
	LDP	EXC_FRAME_X0(RSP), (R2, R3)        // R2 = arg0 (X0), R3 = arg1 (X1)
	LDP	EXC_FRAME_X0+16(RSP), (R4, R5)     // R4 = arg2 (X2), R5 = arg3 (X3)
	LDP	EXC_FRAME_X0+32(RSP), (R6, R7)     // R6 = arg4 (X4), R7 = arg5 (X5)

	// Call using macro: func SyscallDispatch(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64
	GO_CALL_7_1(·SyscallDispatch, R0, R2, R3, R4, R5, R6, R7)
	// R0 now contains return value

	// Store return value back to X0 in exception frame
	MOVD	R0, EXC_FRAME_X0(RSP)

	// Check if rt_sigreturn was called (SigreturnPending flag).
	// If set, the thread's Context has been restored from the signal frame
	// and we need to load it into the exception frame for ERET.
	MOVD	main·CurrentThread(SB), R10
	CBZ	R10, el1_no_sigreturn
	MOVD	main·ThreadSigreturnPendingOffset(SB), R11
	ADD	R11, R10, R11
	MOVW	(R11), R12
	CBZ	R12, el1_no_sigreturn
	// Clear SigreturnPending flag
	MOVW	$0, (R11)
	// Load pointer to ThreadContext
	MOVD	main·ThreadContextOffset(SB), R11
	ADD	R11, R10, R21     // R21 = &thread.Context
	B	copy_context_to_frame  // Reuse existing context-to-frame path
el1_no_sigreturn:

	// Check if syscall handler requested a context switch
	// Call GetSyscallSwitchTarget() - returns 0 if no switch, non-zero for target node pointer
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	MOVD	R0, R20            // R20 = switch target (thread node pointer as uint64)

	// Check if context switch needed (R20 != 0 means switch to that thread node)
	CBZ	R20, syscall_no_switch

	// Context switch requested - call DoContextSwitch(framePtr, targetPtr) to get new context
	MOVD	RSP, R0            // R0 = framePtr (current exception frame)
	MOVD	R20, R1            // R1 = targetPtr (thread node pointer)
	GO_CALL_2_1(·DoContextSwitch, R0, R1)
	MOVD	R0, R21            // R21 = pointer to new ThreadContext

	// R21 = pointer to new ThreadContext — fall through to copy_context_to_frame

copy_context_to_frame:
	// Copy ThreadContext to exception frame, then sync_return will restore it
	// ThreadContext: X[31], SP, ELR, SPSR
	// R21 must point to the ThreadContext to load.
	// Copy X0-X7 (0-64 in ThreadContext, 0-64 in frame)
	LDP	0(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	LDP	16(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+16(RSP)
	LDP	32(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+32(RSP)
	LDP	48(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+48(RSP)

	// Copy X8-X27 (64-224 in ThreadContext, 64-224 in frame)
	LDP	64(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8(RSP)
	LDP	80(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+16(RSP)
	LDP	96(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+32(RSP)
	LDP	112(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+48(RSP)
	LDP	128(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+64(RSP)
	LDP	144(R21), (R0, R1)      // x18, x19
	WORD	$0xf9004be0  // str x0, [sp, #144]  x18
	WORD	$0xf9004fe1  // str x1, [sp, #152]  x19
	LDP	160(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+96(RSP)
	LDP	176(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+112(RSP)
	LDP	192(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+128(RSP)
	LDP	208(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+144(RSP)

	// Copy X28-X30 (224-248 in ThreadContext)
	MOVD	224(R21), R0
	WORD	$0xf90073e0  // str x0, [sp, #224]  x28
	LDP	232(R21), (R0, R1)
	WORD	$0xf90077e0  // str x0, [sp, #232]  x29
	MOVD	R1, EXC_FRAME_X28+16(RSP)  // x30 at frame offset 248

	// Copy SP_EL0 (248 in ThreadContext)
	MOVD	248(R21), R0
	MOVD	R0, EXC_FRAME_SP_EL0(RSP)

	// Copy ELR and SPSR (256, 264 in ThreadContext)
	LDP	256(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_ELR_SPSR(RSP)

	B	sync_return

syscall_no_switch:
	// No context switch, normal return
	B	sync_return

not_svc:
	// Check if this is Data Abort (EC = 0x25 or 0x24)
	CMP	$0x25, R10  // Data Abort from current EL
	BEQ	data_abort
	CMP	$0x24, R10  // Data Abort from lower EL
	BEQ	data_abort

	// Unknown exception - print 'E' with EC and hang
	MOVD	$UART_BASE, R12
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$':', R11
	MOVB	R11, (R12)

	// Save R10 (EC) for later checks
	MOVD	R10, R20  // Save EC in R20

	// Print EC as 2 hex digits
	LSR	$4, R10, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	not_svc_first_digit
	ADD	$('A'-10), R11
	B	not_svc_first
not_svc_first_digit:
	ADD	$'0', R11
not_svc_first:
	MOVB	R11, (R12)

	AND	$0xF, R10
	CMP	$10, R10
	BLT	not_svc_second_digit
	ADD	$('A'-10), R10
	B	not_svc_second
not_svc_second_digit:
	ADD	$'0', R10
not_svc_second:
	MOVB	R10, (R12)

	// Print " EL=" to show current exception level
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)

	// Read CurrentEL register
	MRS	CurrentEL, R11
	LSR	$2, R11, R11   // EL is in bits [3:2]
	AND	$0x3, R11
	ADD	$'0', R11
	MOVB	R11, (R12)

	// Check if this is PC alignment fault (EC=0x22) or instruction abort (EC=0x21) or unknown (EC=0x00)
	CMP	$0x22, R20
	BEQ	print_faulting_pc
	CMP	$0x21, R20
	BEQ	print_faulting_pc
	CMP	$0x00, R20
	BNE	not_pc_align

print_faulting_pc:
	// PC alignment fault or instruction abort - print the faulting ELR
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'P', R11
	MOVB	R11, (R12)
	MOVD	$'C', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'x', R11
	MOVB	R11, (R12)

	// Print ELR (faulting PC) - 16 hex digits
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R14
	MOVD	$16, R15
print_fault_pc_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_fault_pc_digit
	ADD	$('A'-10), R11
	B	print_fault_pc_char
print_fault_pc_digit:
	ADD	$'0', R11
print_fault_pc_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, print_fault_pc_loop

not_pc_align:
	MOVD	$'\r', R11
	MOVB	R11, (R12)
	MOVD	$'\n', R11
	MOVB	R11, (R12)
not_svc_hang:
	B	not_svc_hang

data_abort:
	// Save FAR first
	MRS	FAR_EL1, R19

	// Call HandlePageFaultAsm(faultAddr) to try to handle the page fault
	// func HandlePageFaultAsm(faultAddr uint64) uint64 - returns 1 if handled, 0 if not
	GO_CALL_1_1(·HandlePageFaultAsm, R19)
	// R0 = return value (1 = handled, 0 = not handled)

	// Check if fault was handled
	CMP	$0, R0
	BEQ	data_abort_unhandled

	// Fault handled successfully - return to faulting instruction
	B	sync_return

data_abort_unhandled:
	// Fault not handled - print error info and hang
	MOVD	$UART_BASE, R10
	MOVD	$'F', R11
	MOVB	R11, (R10)
	MOVD	$'A', R11
	MOVB	R11, (R10)
	MOVD	$'I', R11
	MOVB	R11, (R10)
	MOVD	$'L', R11
	MOVB	R11, (R10)

	// Print " EL="
	MOVD	$' ', R11
	MOVB	R11, (R10)
	MOVD	$'E', R11
	MOVB	R11, (R10)
	MOVD	$'L', R11
	MOVB	R11, (R10)
	MOVD	$'=', R11
	MOVB	R11, (R10)

	// Read CurrentEL register and print the exception level
	MRS	CurrentEL, R12
	LSR	$2, R12, R12   // EL is in bits [3:2]
	AND	$0x3, R12
	ADD	$'0', R12
	MOVB	R12, (R10)

	// Print " FAR="
	MOVD	$' ', R11
	MOVB	R11, (R10)
	MOVD	$'F', R11
	MOVB	R11, (R10)
	MOVD	$'A', R11
	MOVB	R11, (R10)
	MOVD	$'R', R11
	MOVB	R11, (R10)
	MOVD	$'=', R11
	MOVB	R11, (R10)

	// Print FAR (fault address) - stored in R19 from earlier
	MOVD	R19, R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_far_data_abort:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_far_digit_da
	ADD	$('A'-10), R11
	B	print_far_char_da
print_far_digit_da:
	ADD	$'0', R11
print_far_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_far_data_abort

	// Print " ELR="
	MOVD	$' ', R11
	MOVB	R11, (R10)
	MOVD	$'E', R11
	MOVB	R11, (R10)
	MOVD	$'L', R11
	MOVB	R11, (R10)
	MOVD	$'R', R11
	MOVB	R11, (R10)
	MOVD	$'=', R11
	MOVB	R11, (R10)

	// Print ELR (faulting instruction)
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R12
	MOVD	$16, R13
print_elr_data_abort:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_elr_digit_da
	ADD	$('A'-10), R11
	B	print_elr_char_da
print_elr_digit_da:
	ADD	$'0', R11
print_elr_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_elr_data_abort

	// Print extra debug: X0 from exception frame (unwinder ptr) and key fields
	MOVD	$'\r', R11
	MOVB	R11, (R10)
	MOVD	$'\n', R11
	MOVB	R11, (R10)

	// Load saved X0 from exception frame
	MOVD	EXC_FRAME_X0(RSP), R14

	// Print "X0="
	MOVD	$'X', R11; MOVB	R11, (R10)
	MOVD	$'0', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	R14, R12
	MOVD	$16, R13
print_x0_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_x0_digit_da
	ADD	$('A'-10), R11
	B	print_x0_char_da
print_x0_digit_da:
	ADD	$'0', R11
print_x0_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_x0_da

	// If X0 looks valid (high kernel addr), print [X0+16] (unwinder.frame.pc) and [X0+40] (sp)
	// Check if X0 > 0xFFFFFFFF40000000
	MOVD	$0xFFFFFFFF40000000, R15
	CMP	R15, R14
	BLO	skip_unwinder_dump

	// Print " PC="
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'P', R11; MOVB	R11, (R10)
	MOVD	$'C', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	16(R14), R12     // frame.pc
	MOVD	$16, R13
print_pc_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_pc_digit_da
	ADD	$('A'-10), R11
	B	print_pc_char_da
print_pc_digit_da:
	ADD	$'0', R11
print_pc_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_pc_da

	// Print " SP="
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'S', R11; MOVB	R11, (R10)
	MOVD	$'P', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	40(R14), R12     // frame.sp
	MOVD	$16, R13
print_sp_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_sp_digit_da
	ADD	$('A'-10), R11
	B	print_sp_char_da
print_sp_digit_da:
	ADD	$'0', R11
print_sp_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_sp_da

	// Print " G="
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'G', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	72(R14), R12     // unwinder.g
	MOVD	$16, R13
print_g_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_g_digit_da
	ADD	$('A'-10), R11
	B	print_g_char_da
print_g_digit_da:
	ADD	$'0', R11
print_g_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_g_da

	// Print " SP0="
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'S', R11; MOVB	R11, (R10)
	MOVD	$'P', R11; MOVB	R11, (R10)
	MOVD	$'0', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	EXC_FRAME_SP_EL0(RSP), R12   // SP_EL0 at exception time
	MOVD	$16, R13
print_sp0_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_sp0_digit_da
	ADD	$('A'-10), R11
	B	print_sp0_char_da
print_sp0_digit_da:
	ADD	$'0', R11
print_sp0_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_sp0_da

	// Print " LR="
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'L', R11; MOVB	R11, (R10)
	MOVD	$'R', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	(EXC_FRAME_X29_X30+8)(RSP), R12   // X30 = LR at exception time
	MOVD	$16, R13
print_lr_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_lr_digit_da
	ADD	$('A'-10), R11
	B	print_lr_char_da
print_lr_digit_da:
	ADD	$'0', R11
print_lr_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_lr_da

	// Print " R28=" (g register)
	MOVD	$' ', R11; MOVB	R11, (R10)
	MOVD	$'R', R11; MOVB	R11, (R10)
	MOVD	$'2', R11; MOVB	R11, (R10)
	MOVD	$'8', R11; MOVB	R11, (R10)
	MOVD	$'=', R11; MOVB	R11, (R10)
	MOVD	EXC_FRAME_X28(RSP), R12   // R28 = g register at exception time
	MOVD	$16, R13
print_r28_da:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_r28_digit_da
	ADD	$('A'-10), R11
	B	print_r28_char_da
print_r28_digit_da:
	ADD	$'0', R11
print_r28_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_r28_da

	MOVD	$'\r', R11; MOVB	R11, (R10)
	MOVD	$'\n', R11; MOVB	R11, (R10)

skip_unwinder_dump:
data_abort_hang:
	B	data_abort_hang

sync_return:
	// Clear svcDepth — we're leaving the SVC handler (safe to preempt again).
	// IRQs are masked (DAIF.I set during exception handling), so no race with timer.
	MOVW	ZR, ·svcDepth(SB)

	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	// MSR SP_EL0, X10 - use WORD to avoid assembler issues
	WORD	$0xD518410A

	// Restore ELR and SPSR
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	// CRITICAL: Force IRQs enabled in SPSR by clearing DAIF.I bit (bit 7 = 0x80)
	// This ensures IRQs are enabled after ERET, preventing stuck-disabled-IRQ chains
	BIC	$0x80, R11, R11
	// MSR ELR_EL1, X10 - use WORD to avoid assembler issues
	WORD	$0xD518402A
	// MSR SPSR_EL1, X11 - use WORD to avoid assembler issues
	WORD	$0xD518400B

	// Restore X28-X30 (R28 is g, use raw instruction)
	// ldr x28, [sp, #224]
	WORD	$0xf94073fc  // ldr x28, [sp, #224] - NOTE: 73fc not 70fc (Rn=31=sp)
	// ldr x29, [sp, #232]
	WORD	$0xf94077fd  // ldr x29, [sp, #232] - NOTE: 77fd not 74fd (Rn=31=sp)
	MOVD	EXC_FRAME_X28+16(RSP), R10
	MOVD	R10, LR

	// Restore X8-X27
	LDP	EXC_FRAME_X8(RSP), (R8, R9)
	LDP	EXC_FRAME_X8+16(RSP), (R10, R11)
	LDP	EXC_FRAME_X8+32(RSP), (R12, R13)
	LDP	EXC_FRAME_X8+48(RSP), (R14, R15)
	LDP	EXC_FRAME_X8+64(RSP), (R16, R17)
	// R18 is platform register, use raw instruction: ldr x18, [sp, #144]
	WORD	$0xf9404bf2  // ldr x18, [sp, #144]
	// Restore R19: ldr x19, [sp, #152]
	WORD	$0xf9404ff3  // ldr x19, [sp, #152]
	LDP	EXC_FRAME_X8+96(RSP), (R20, R21)
	LDP	EXC_FRAME_X8+112(RSP), (R22, R23)
	LDP	EXC_FRAME_X8+128(RSP), (R24, R25)
	LDP	EXC_FRAME_X8+144(RSP), (R26, R27)

	// Restore X0-X7
	LDP	EXC_FRAME_X0(RSP), (R0, R1)
	LDP	EXC_FRAME_X0+16(RSP), (R2, R3)
	LDP	EXC_FRAME_X0+32(RSP), (R4, R5)
	LDP	EXC_FRAME_X0+48(RSP), (R6, R7)

	// Clean up stack and return
	ADD	$EXC_FRAME_SIZE, RSP
	ERET

// ============================================================================
// irq_exception_handler - Hardware interrupts (timer, UART, etc.)
// ============================================================================
// NOTE: Use high-memory GIC address since kmazarin runs at high memory
// Cardinal maps GIC at 0xFFFFFFFF08000000 before jumping to us
#define GIC_CPU_BASE	0xFFFFFFFF08010000
#define GICC_IAR	0x000C  // Interrupt Acknowledge Register offset
#define GICC_EOIR	0x0010  // End Of Interrupt Register offset

irq_exception_handler:
	// CRITICAL: Switch to SP_EL1 IMMEDIATELY before using RSP for anything.
	// When an exception is taken from EL1t mode (Go code using SP_EL0),
	// ARM64 preserves PSTATE.SP=0, meaning RSP still aliases SP_EL0!
	// Without this switch, we'd corrupt the interrupted code's stack.
	MSR	$1, SPSel

	// CRITICAL: Save X0-X7 BEFORE any debug output!
	// These may contain important values that must not be clobbered.
	// Allocate frame first
	SUB	$EXC_FRAME_SIZE, RSP

	// Save X0-X7 IMMEDIATELY (before debug print clobbers them!)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	STP	(R2, R3), EXC_FRAME_X0+16(RSP)
	STP	(R4, R5), EXC_FRAME_X0+32(RSP)
	STP	(R6, R7), EXC_FRAME_X0+48(RSP)

	// Save X8-X27
	STP	(R8, R9), EXC_FRAME_X8(RSP)
	STP	(R10, R11), EXC_FRAME_X8+16(RSP)
	STP	(R12, R13), EXC_FRAME_X8+32(RSP)
	STP	(R14, R15), EXC_FRAME_X8+48(RSP)
	STP	(R16, R17), EXC_FRAME_X8+64(RSP)
	// R18 is platform register, use raw instruction: str x18, [sp, #144]
	WORD	$0xf9004bf2  // str x18, [sp, #144]
	// Save R19: str x19, [sp, #152]
	WORD	$0xf9004ff3  // str x19, [sp, #152]
	STP	(R20, R21), EXC_FRAME_X8+96(RSP)
	STP	(R22, R23), EXC_FRAME_X8+112(RSP)
	STP	(R24, R25), EXC_FRAME_X8+128(RSP)
	STP	(R26, R27), EXC_FRAME_X8+144(RSP)

	// Save X28-X30 (R28 is g, use raw instruction)
	// str x28, [sp, #224]
	WORD	$0xf90073fc  // str x28, [sp, #224]
	// str x29, [sp, #232]
	WORD	$0xf90077fd  // str x29, [sp, #232]
	MOVD	LR, R10
	MOVD	R10, EXC_FRAME_X28+16(RSP)

	// Save ELR, SPSR, FAR, ESR
	MRS	ELR_EL1, R10
	MRS	SPSR_EL1, R11
	STP	(R10, R11), EXC_FRAME_ELR_SPSR(RSP)

	MRS	FAR_EL1, R10
	MRS	ESR_EL1, R11
	STP	(R10, R11), EXC_FRAME_FAR_ESR(RSP)

	// Save SP_EL0
	MRS	SP_EL0, R10
	MOVD	R10, EXC_FRAME_SP_EL0(RSP)

	// Read interrupt number from GIC CPU interface
	// IAR = GICC_BASE + 0x0C
	MOVD	$(GIC_CPU_BASE + GICC_IAR), R10
	MOVW	(R10), R0  // R0 = interrupt number

	// Store IAR value for later EOIR write
	MOVD	R0, R19  // Save full IAR value in R19

	// Mask to get interrupt ID (bits 0-9, max 1020 for GICv2)
	AND	$0x3FF, R0  // R0 = IRQ number (0-1019)

	// Check if this is the timer IRQ (27)
	CMP	$27, R0
	BNE	irq_not_timer

	// ========================================================================
	// Timer IRQ (27) - Call pure assembly handler
	// ========================================================================
	// The timer handler sets g.preempt and g.stackguard0 directly without
	// calling any Go functions. This is safe because we're not using the
	// Go runtime at all - just setting memory locations.
	//
	// The handler checks thread preemption deadline and sets
	// NeedsThreadPreempt if a thread has exceeded its time quantum.
	CALL	mazzy∕kmazarin∕kirq·TimerIRQHandlerAsm(SB)

	// ========================================================================
	// CRITICAL: Write GICC_EOIR for timer IRQ IMMEDIATELY after handler
	// ========================================================================
	// The timer handler clears the interrupt condition (updates CNTV_CVAL_EL0),
	// so it's safe to write EOIR now. Without this, the timer IRQ stays ACTIVE
	// and no new timer interrupts can be delivered, breaking preemption.
	//
	// R19 contains the full IAR value saved at the start of the IRQ handler.

	MOVD	$(GIC_CPU_BASE + GICC_EOIR), R10
	MOVW	R19, (R10)  // Write IAR value to EOIR

	// ========================================================================
	// Process deadline queue in top-half context
	// ========================================================================
	// CRITICAL: Must run ProcessDeadlines HERE (not in bottom half) because
	// bottom half goroutines run on kernel threads that may be blocked on futex.
	// If all kernel threads are blocked, the bottom half never runs, and timed
	// futex waits / nanosleep deadlines never fire — starving the kernel.
	// ProcessDeadlines moves expired threads to the ready queue so they can
	// be picked by the next scheduling decision.
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, skip_deadline_processing  // g0 not ready yet

	// Set m.curg = g0 so Go runtime sees getg() == m.curg (avoids "defer on system stack").
	MOVD	48(R10), R11           // R11 = g0.m = m0
	CBZ	R11, skip_curg_set
	MOVD	0xB8(R11), R19         // R19 = saved m0.curg
	MOVD	R11, R20               // R20 = saved m0 pointer
	MOVD	R10, 0xB8(R11)         // m0.curg = g0
skip_curg_set:

	WORD	$0xaa0a03fc  // mov x28, x10 — set g to kmazarin g0

	CALL	·ProcessDeadlinesTopHalf(SB)

	// Restore m0.curg
	CBZ	R20, skip_deadline_processing
	MOVD	R19, 0xB8(R20)         // m0.curg = saved value
skip_deadline_processing:

	// ========================================================================
	// Thread preemption check - switch to another thread if threshold exceeded
	// ========================================================================
	// Check if the interrupted code was in kernel mode (EL1).
	// If EL1: only preempt when SVCDepth==0 (running normal Go code, not
	// inside an SVC handler where the exception stack has a live frame).
	// If EL0: always eligible for preemption (userspace).
	MOVD	(EXC_FRAME_ELR_SPSR+8)(RSP), R10	// R10 = saved SPSR
	AND	$0x4, R10, R10				// EL1 bit (M[2])
	CBZ	R10, timer_check_preemption		// EL0 — always check

	// EL1: check svcDepth — only safe to preempt when depth==0
	MOVW	·svcDepth(SB), R10
	CBNZ	R10, timer_no_thread_preempt		// depth=1, inside SVC — skip

	// EL1, depth=0 — safe to preempt kernel thread
timer_check_preemption:

	// Check NeedsThreadPreempt flag set by TimerIRQHandlerAsm
	MOVW	mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB), R10
	CBZ	R10, timer_no_thread_preempt

	// NOTE: m.locks check removed for EL0 (userspace) thread preemption.
	// Each priest runs in its own address space with isolated Go runtime state.
	// Context-switching freezes and restores the full CPU state atomically —
	// the priest resumes exactly where it was interrupted, locks intact.
	// The SPSR/EL1 check above already filters out kernel-mode preemption.

	// Clear NeedsThreadPreempt flag
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB)

	// CRITICAL: Switch to kmazarin's g before calling Go code
	// The timer may have interrupted userspace (priest) which has a different g
	MOVD	·kmazarinG0Addr(SB), R10
	CBNZ	R10, g0_addr_ok
	B	timer_no_thread_preempt  // Skip if not initialized
g0_addr_ok:
	// Set m.curg = g0 (same fix as ProcessDeadlinesTopHalf)
	// R19/R20 may hold values from ProcessDeadlinesTopHalf but that section
	// already restored them, so re-save here.
	MOVD	48(R10), R11           // R11 = g0.m = m0
	CBZ	R11, timer_no_thread_preempt
	MOVD	0xB8(R11), R19         // R19 = saved m0.curg
	MOVD	R11, R20               // R20 = saved m0 ptr
	MOVD	R10, 0xB8(R11)         // m0.curg = g0

	WORD	$0xaa0a03fc  // mov x28, x10

	// Call CheckThreadPreemption(framePtr) to check and perform switch
	// func CheckThreadPreemption(framePtr uint64) uint64
	MOVD	RSP, R0                        // R0 = framePtr (exception frame)
	GO_CALL_1_1(·CheckThreadPreemption, R0)

	// Restore m0.curg
	MOVD	R19, 0xB8(R20)         // m0.curg = saved value

	MOVD	R0, R21                        // R21 = new context pointer (or 0)

	// Check if context switch happened
	CBNZ	R21, timer_switch_ok

	B	timer_no_thread_preempt

timer_switch_ok:

	// Context switch happened - copy new ThreadContext to exception frame
	// ThreadContext layout: X[31]*8=248 bytes, SP(8), ELR(8), SPSR(8) = 272 bytes total

	// Copy X0-X7 (0-64 in ThreadContext)
	LDP	0(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	LDP	16(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+16(RSP)
	LDP	32(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+32(RSP)
	LDP	48(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+48(RSP)

	// Copy X8-X27 (64-224 in ThreadContext)
	LDP	64(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8(RSP)
	LDP	80(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+16(RSP)
	LDP	96(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+32(RSP)
	LDP	112(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+48(RSP)
	LDP	128(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+64(RSP)
	LDP	144(R21), (R0, R1)      // x18, x19
	WORD	$0xf9004be0  // str x0, [sp, #144]  x18
	WORD	$0xf9004fe1  // str x1, [sp, #152]  x19
	LDP	160(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+96(RSP)
	LDP	176(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+112(RSP)
	LDP	192(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+128(RSP)
	LDP	208(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+144(RSP)

	// Copy X28-X30 (224-248 in ThreadContext)
	MOVD	224(R21), R0
	WORD	$0xf90073e0  // str x0, [sp, #224]  x28
	LDP	232(R21), (R0, R1)
	WORD	$0xf90077e0  // str x0, [sp, #232]  x29
	MOVD	R1, EXC_FRAME_X28+16(RSP)  // x30 at frame offset 248

	// Copy SP_EL0 (248 in ThreadContext)
	MOVD	248(R21), R0
	MOVD	R0, EXC_FRAME_SP_EL0(RSP)

	// Copy ELR and SPSR (256, 264 in ThreadContext)
	LDP	256(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_ELR_SPSR(RSP)

	// Skip async preemption - we already switched threads
	B	timer_no_preempt

timer_no_thread_preempt:
timer_no_preempt:
	// Goroutine-level preemption is now handled by the Go runtime in userspace
	// via SIGURG signals. The kernel only does thread-level preemption.
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23

	B	irq_write_eoir

irq_not_timer:
	// ========================================================================
	// Non-timer IRQs - Set pending flag for bottom-half processing
	// ========================================================================
	// R0 contains the masked IRQ number, R19 contains IAR value

	// Write EOIR immediately so GIC can deliver the next interrupt.
	MOVD	$(GIC_CPU_BASE + GICC_EOIR), R10
	MOVW	R19, (R10)

	// Check if IRQ number is in valid range (0-1019)
	CMP	$1020, R0
	BGE	irq_invalid

	// Call Go top-half handler directly with kmazarin g0 context.
	// Store IRQ number in global before calling (ABI0 — no register args).
	MOVD	R0, ·topHalfIRQNum(SB)
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, irq_skip_dispatch  // g0 not ready yet

	// Set m.curg = g0 (same fix as timer IRQ handler)
	MOVD	48(R10), R11           // R11 = g0.m = m0
	CBZ	R11, irq_skip_curg_set
	MOVD	0xB8(R11), R19         // R19 = saved m0.curg
	MOVD	R11, R20               // R20 = saved m0 ptr
	MOVD	R10, 0xB8(R11)         // m0.curg = g0
irq_skip_curg_set:

	WORD	$0xaa0a03fc  // mov x28, x10 — set g to kmazarin g0
	CALL	·NonTimerIRQTopHalf(SB)

	// Restore m0.curg
	CBZ	R20, irq_skip_dispatch
	MOVD	R19, 0xB8(R20)         // m0.curg = saved value

irq_skip_dispatch:

	// Non-timer IRQs don't trigger preemption (only timer does)
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23
	B	irq_write_eoir

irq_invalid:
	// Invalid IRQ number - just acknowledge and return
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23

irq_write_eoir:
	// NOTE: Do NOT write GICC_EOIR here!
	// For level-triggered interrupts (UART TX), writing EOIR while the condition
	// is still true causes an immediate re-fire, creating an interrupt storm.
	// The bottom-half dispatcher will write EOIR after the handler clears the condition.

irq_return:
	// NOTE: Do NOT write GICC_EOIR here for level-triggered interrupts!
	// For level-triggered interrupts (like UART TX), the interrupt condition
	// may still be true (e.g., TX FIFO has space). Writing EOIR now would
	// cause the GIC to immediately re-fire the interrupt, creating a storm.
	// Instead, the bottom-half writes EOIR after clearing the interrupt condition.

	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR (same pattern as el0_return which works correctly)
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	MSR	R10, ELR_EL1
	MSR	R11, SPSR_EL1

	// Restore X28-X30 (R28 is g, use raw instruction)
	// ldr x28, [sp, #224]
	WORD	$0xf94073fc  // ldr x28, [sp, #224] - NOTE: 73fc not 70fc (Rn=31=sp)
	// ldr x29, [sp, #232]
	WORD	$0xf94077fd  // ldr x29, [sp, #232] - NOTE: 77fd not 74fd (Rn=31=sp)
	MOVD	EXC_FRAME_X28+16(RSP), R10
	MOVD	R10, LR

	// Restore X8-X27
	LDP	EXC_FRAME_X8(RSP), (R8, R9)
	LDP	EXC_FRAME_X8+16(RSP), (R10, R11)
	LDP	EXC_FRAME_X8+32(RSP), (R12, R13)
	LDP	EXC_FRAME_X8+48(RSP), (R14, R15)
	LDP	EXC_FRAME_X8+64(RSP), (R16, R17)
	// R18 is platform register, use raw instruction: ldr x18, [sp, #144]
	WORD	$0xf9404bf2  // ldr x18, [sp, #144]
	// Restore R19: ldr x19, [sp, #152]
	WORD	$0xf9404ff3  // ldr x19, [sp, #152]
	LDP	EXC_FRAME_X8+96(RSP), (R20, R21)
	LDP	EXC_FRAME_X8+112(RSP), (R22, R23)
	LDP	EXC_FRAME_X8+128(RSP), (R24, R25)
	LDP	EXC_FRAME_X8+144(RSP), (R26, R27)

	// Restore X0-X7
	LDP	EXC_FRAME_X0(RSP), (R0, R1)
	LDP	EXC_FRAME_X0+16(RSP), (R2, R3)
	LDP	EXC_FRAME_X0+32(RSP), (R4, R5)
	LDP	EXC_FRAME_X0+48(RSP), (R6, R7)

	// Clean up stack and return
	ADD	$EXC_FRAME_SIZE, RSP
	ERET

// ============================================================================
// el0_sync_handler - Synchronous exceptions from EL0 (userspace)
// ============================================================================
// Handles SVC syscalls from userspace programs (priest, etc.)
// Very similar to sync_exception_handler but for EL0 origin.
//
el0_sync_handler:
	// We're already using SP_EL1 when taking exception from EL0
	// (ARM64 automatically switches to SP_EL1 for EL1 handlers)

	// Allocate exception frame
	SUB	$EXC_FRAME_SIZE, RSP

	// Save X0-X7 (syscall arguments)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	STP	(R2, R3), EXC_FRAME_X0+16(RSP)
	STP	(R4, R5), EXC_FRAME_X0+32(RSP)
	STP	(R6, R7), EXC_FRAME_X0+48(RSP)

	// Save X8-X27
	STP	(R8, R9), EXC_FRAME_X8(RSP)
	STP	(R10, R11), EXC_FRAME_X8+16(RSP)
	STP	(R12, R13), EXC_FRAME_X8+32(RSP)
	STP	(R14, R15), EXC_FRAME_X8+48(RSP)
	STP	(R16, R17), EXC_FRAME_X8+64(RSP)
	WORD	$0xf9004bf2  // str x18, [sp, #144]
	WORD	$0xf9004ff3  // str x19, [sp, #152]
	STP	(R20, R21), EXC_FRAME_X8+96(RSP)
	STP	(R22, R23), EXC_FRAME_X8+112(RSP)
	STP	(R24, R25), EXC_FRAME_X8+128(RSP)
	STP	(R26, R27), EXC_FRAME_X8+144(RSP)

	// Save X28-X30
	WORD	$0xf90073fc  // str x28, [sp, #224]
	WORD	$0xf90077fd  // str x29, [sp, #232]
	MOVD	LR, R10
	MOVD	R10, EXC_FRAME_X28+16(RSP)

	// Save ELR, SPSR, FAR, ESR
	MRS	ELR_EL1, R10
	MRS	SPSR_EL1, R11
	STP	(R10, R11), EXC_FRAME_ELR_SPSR(RSP)

	MRS	FAR_EL1, R10
	MRS	ESR_EL1, R11
	STP	(R10, R11), EXC_FRAME_FAR_ESR(RSP)

	// Save SP_EL0 (userspace stack pointer)
	MRS	SP_EL0, R10
	MOVD	R10, EXC_FRAME_SP_EL0(RSP)

	// Extract exception class (EC) from ESR_EL1
	MOVD	EXC_FRAME_FAR_ESR+8(RSP), R10
	LSR	$26, R10, R10
	AND	$0x3F, R10

	// Check if this is SVC (EC = 0x15)
	CMP	$0x15, R10
	BNE	el0_check_data_abort

	// CRITICAL: Switch to kmazarin's g before calling any Go code!
	// x28 currently contains userspace's g (e.g., priest's g), but the syscall
	// handlers are compiled into kmazarin's Go runtime and expect kmazarin's g.
	// We saved userspace's g in the exception frame, so we can load kmazarin's g0.
	// Only switch if kmazarinG0Addr is non-zero (initialized).
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, skip_g_switch_el0  // Skip if not initialized
	// R10 now contains kmazarin's g address (stored at init time)
	// Set x28 to this value (x28 is Go's g register)
	WORD	$0xaa0a03fc  // mov x28, x10
skip_g_switch_el0:
	// SVC from userspace - first save ELR and SPSR for clone
	// Without this, clone would use stale values from a previous EL1 syscall!
	// CRITICAL: R0-R17 are caller-saved and will be clobbered by function calls!
	// We must save SPSR to a callee-saved register (R19) before calling SetSyscallELR.
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R0, R19)  // Load ELR into R0, SPSR into R19 (callee-saved)
	GO_CALL_1_0(·SetSyscallELR, R0)          // SetSyscallELR(elr) - may clobber R0-R17
	GO_CALL_1_0(·SetSyscallSPSR, R19)        // SetSyscallSPSR(spsr) - R19 preserved

	// Clear InCloneSetup flag for current thread (if set)
	// This marks the clone child as having completed its setup phase.
	// After this, async preempt is safe because fn/gp/mp have been read from stack.
	MOVD	main·CurrentThread(SB), R10
	CBZ	R10, el0_skip_clear_clone_setup
	MOVD	main·ThreadInCloneSetupOffset(SB), R11
	ADD	R11, R10, R11
	MOVW	$0, R12
	MOVW	R12, (R11)  // thread.InCloneSetup = 0
el0_skip_clear_clone_setup:

	// Now dispatch syscall
	// Load arguments from exception frame
	LDP	EXC_FRAME_X8(RSP), (R0, R1)        // R0 = syscall num (X8)
	LDP	EXC_FRAME_X0(RSP), (R2, R3)        // R2 = arg0, R3 = arg1
	LDP	EXC_FRAME_X0+16(RSP), (R4, R5)     // R4 = arg2, R5 = arg3
	LDP	EXC_FRAME_X0+32(RSP), (R6, R7)     // R6 = arg4, R7 = arg5


	// Re-load args (R14/R15 were scratch)
	LDP	EXC_FRAME_X8(RSP), (R0, R1)        // R0 = syscall num (X8)
	LDP	EXC_FRAME_X0(RSP), (R2, R3)        // R2 = arg0, R3 = arg1
	LDP	EXC_FRAME_X0+16(RSP), (R4, R5)     // R4 = arg2, R5 = arg3
	LDP	EXC_FRAME_X0+32(RSP), (R6, R7)     // R6 = arg4, R7 = arg5

	// Call syscall dispatcher
	GO_CALL_7_1(·SyscallDispatch, R0, R2, R3, R4, R5, R6, R7)

	// Store return value back to X0 in exception frame
	MOVD	R0, EXC_FRAME_X0(RSP)

	// Check if rt_sigreturn was called (SigreturnPending flag).
	// If set, the thread's Context has been restored from the signal frame
	// and we need to load it into the exception frame for ERET.
	MOVD	main·CurrentThread(SB), R10
	CBZ	R10, el0_no_sigreturn
	MOVD	main·ThreadSigreturnPendingOffset(SB), R11
	ADD	R11, R10, R11
	MOVW	(R11), R12
	CBZ	R12, el0_no_sigreturn
	// Clear SigreturnPending flag
	MOVW	$0, (R11)
	// Load pointer to ThreadContext
	MOVD	main·ThreadContextOffset(SB), R11
	ADD	R11, R10, R21     // R21 = &thread.Context
	B	el0_copy_context_to_frame
el0_no_sigreturn:

	// Check if syscall handler requested a context switch
	// Call GetSyscallSwitchTarget() - returns 0 if no switch, non-zero for target node pointer
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	MOVD	R0, R20            // R20 = switch target (thread node pointer as uint64)

	// Check if context switch needed (R20 != 0 means switch to that thread node)
	CBZ	R20, el0_no_switch

	// Context switch requested - call DoContextSwitch(framePtr, targetPtr) to get new context
	MOVD	RSP, R0            // R0 = framePtr (current exception frame)
	MOVD	R20, R1            // R1 = targetPtr (thread node pointer)
	GO_CALL_2_1(·DoContextSwitch, R0, R1)
	MOVD	R0, R21            // R21 = pointer to new ThreadContext

	// R21 = pointer to new ThreadContext — fall through to el0_copy_context_to_frame

el0_copy_context_to_frame:
	// Copy ThreadContext to exception frame, then el0_return will restore it
	// ThreadContext: X[31], SP, ELR, SPSR
	// R21 must point to the ThreadContext to load.
	// Copy X0-X7 (0-64 in ThreadContext, 0-64 in frame)
	LDP	0(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0(RSP)
	LDP	16(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+16(RSP)
	LDP	32(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+32(RSP)
	LDP	48(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X0+48(RSP)

	// Copy X8-X27 (64-224 in ThreadContext, 64-224 in frame)
	LDP	64(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8(RSP)
	LDP	80(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+16(RSP)
	LDP	96(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+32(RSP)
	LDP	112(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+48(RSP)
	LDP	128(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+64(RSP)
	LDP	144(R21), (R0, R1)      // x18, x19
	WORD	$0xf9004be0  // str x0, [sp, #144]  x18
	WORD	$0xf9004fe1  // str x1, [sp, #152]  x19
	LDP	160(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+96(RSP)
	LDP	176(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+112(RSP)
	LDP	192(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+128(RSP)
	LDP	208(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_X8+144(RSP)

	// Copy X28-X30 (224-248 in ThreadContext)
	MOVD	224(R21), R0
	WORD	$0xf90073e0  // str x0, [sp, #224]  x28
	LDP	232(R21), (R0, R1)
	WORD	$0xf90077e0  // str x0, [sp, #232]  x29
	MOVD	R1, EXC_FRAME_X28+16(RSP)  // x30 at frame offset 248

	// Copy SP_EL0 (248 in ThreadContext)
	MOVD	248(R21), R0
	MOVD	R0, EXC_FRAME_SP_EL0(RSP)

	// Copy ELR and SPSR (256, 264 in ThreadContext)
	LDP	256(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_ELR_SPSR(RSP)

	B	el0_return

el0_no_switch:
	// No context switch, normal return
	B	el0_return

el0_check_data_abort:
	// Check if this is Instruction Abort from lower EL (EC = 0x20)
	CMP	$0x20, R10
	BEQ	el0_handle_page_fault

	// Check if this is Data Abort from lower EL (EC = 0x24)
	CMP	$0x24, R10
	BNE	el0_not_svc

el0_handle_page_fault:
	// CRITICAL: Switch to kmazarin's g before calling Go code (same as SVC case)
	// Only switch if kmazarinG0Addr is non-zero (initialized).
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, skip_g_switch_el0_da
	WORD	$0xaa0a03fc  // mov x28, x10
skip_g_switch_el0_da:

	// Data Abort from userspace - try to handle page fault
	// Get fault address from FAR_EL1 (already in exception frame)
	MOVD	EXC_FRAME_FAR_ESR(RSP), R19

	// Compute isPermFault from ESR DFSC bits [5:0].
	// Permission faults have DFSC[5:2] == 0b0011 (values 0x0C-0x0F for levels 0-3).
	// Shift ESR right 2 bits, mask to 4 bits → value 3 means permission fault.
	MOVD	EXC_FRAME_FAR_ESR+8(RSP), R20  // R20 = ESR
	LSR	$2, R20, R20                    // R20 = ESR >> 2 → DFSC[5:2] in bits [3:0]
	AND	$0xF, R20                       // R20 = DFSC[5:2] & 0xF
	CMP	$3, R20                         // DFSC[5:2] == 3 → permission fault?
	MOVD	$0, R20
	BNE	el0_upf_not_perm
	MOVD	$1, R20                         // isPermFault = 1
el0_upf_not_perm:

	// Call HandleUserPageFaultAsm(faultAddr, isPermFault) to try to handle the fault.
	// func HandleUserPageFaultAsm(faultAddr, isPermFault uint64) uint64
	GO_CALL_2_1(·HandleUserPageFaultAsm, R19, R20)
	// R0 = return value (1 = handled, 0 = not handled)

	// Check if fault was handled
	CMP	$0, R0
	BEQ	el0_data_abort_unhandled

	// Fault handled successfully - return to faulting instruction
	B	el0_return

el0_data_abort_unhandled:
	// Fault not handled - fall through to error print

el0_not_svc:
	// Non-SVC exception from userspace - print error and halt
	// DEBUG: Print ELR from exception frame (saved at entry, not corrupted by nested calls)
	MOVD	$UART_BASE, R12
	MOVD	$'[', R11
	MOVB	R11, (R12)
	MOVD	$'F', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'M', R11
	MOVB	R11, (R12)
	MOVD	$':', R11
	MOVB	R11, (R12)
	// Also print ESR from frame for full diagnosis
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$'S', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	EXC_FRAME_FAR_ESR+8(RSP), R14  // ESR from exception frame
	MOVD	$8, R15  // 8 hex digits for ESR
print_real_esr:
	LSR	$28, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_real_esr_d
	ADD	$('A'-10), R11
	B	print_real_esr_c
print_real_esr_d:
	ADD	$'0', R11
print_real_esr_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, print_real_esr
	MOVD	$' ', R11
	MOVB	R11, (R12)
	// Now print ELR from exception frame (saved at entry)
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R14
	MOVD	$16, R15
print_real_elr:
	LSR	$60, R14, R11
	CMP	$10, R11
	BLT	print_real_elr_d
	ADD	$('A'-10), R11
	B	print_real_elr_c
print_real_elr_d:
	ADD	$'0', R11
print_real_elr_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, print_real_elr
	MOVD	$']', R11
	MOVB	R11, (R12)
	// End DEBUG
	MOVD	$'U', R11
	MOVB	R11, (R12)
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$':', R11
	MOVB	R11, (R12)

	// Reload EC (may have been clobbered by GO_CALL)
	MOVD	EXC_FRAME_FAR_ESR+8(RSP), R10
	LSR	$26, R10, R10
	AND	$0x3F, R10

	// Print EC
	LSR	$4, R10, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_not_svc_digit1
	ADD	$('A'-10), R11
	B	el0_not_svc_char1
el0_not_svc_digit1:
	ADD	$'0', R11
el0_not_svc_char1:
	MOVB	R11, (R12)

	AND	$0xF, R10
	CMP	$10, R10
	BLT	el0_not_svc_digit2
	ADD	$('A'-10), R10
	B	el0_not_svc_char2
el0_not_svc_digit2:
	ADD	$'0', R10
el0_not_svc_char2:
	MOVB	R10, (R12)

	// Print " FAR="
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'F', R11
	MOVB	R11, (R12)
	MOVD	$'A', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'x', R11
	MOVB	R11, (R12)

	// Print FAR_EL1 (16 hex digits)
	MOVD	EXC_FRAME_FAR_ESR(RSP), R14
	MOVD	$16, R15
el0_print_far_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_far_digit
	ADD	$('A'-10), R11
	B	el0_far_char
el0_far_digit:
	ADD	$'0', R11
el0_far_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_far_loop

	// Print " ELR="
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'E', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'x', R11
	MOVB	R11, (R12)

	// Print ELR_EL1 (16 hex digits)
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R14
	MOVD	$16, R15
el0_print_elr_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_elr_digit
	ADD	$('A'-10), R11
	B	el0_elr_char
el0_elr_digit:
	ADD	$'0', R11
el0_elr_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_elr_loop

	// Print " x28="
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'x', R11
	MOVB	R11, (R12)
	MOVD	$'2', R11
	MOVB	R11, (R12)
	MOVD	$'8', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)

	// Print x28 from saved context (16 hex digits)
	MOVD	EXC_FRAME_X28(RSP), R14
	MOVD	$16, R15
el0_print_x28_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_x28_digit
	ADD	$('A'-10), R11
	B	el0_x28_char
el0_x28_digit:
	ADD	$'0', R11
el0_x28_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_x28_loop

	// Print " SPSR="
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'S', R11
	MOVB	R11, (R12)
	MOVD	$'P', R11
	MOVB	R11, (R12)
	MOVD	$'S', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)

	// Print SPSR from exception frame (16 hex digits)
	MOVD	EXC_FRAME_ELR_SPSR+8(RSP), R14
	MOVD	$16, R15
el0_print_spsr_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_spsr_digit
	ADD	$('A'-10), R11
	B	el0_spsr_char
el0_spsr_digit:
	ADD	$'0', R11
el0_spsr_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_spsr_loop

	// Print " LR=" (X30 at time of fault — tells us who called/jumped to the faulting address)
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	(EXC_FRAME_X28+16)(RSP), R14  // X30 = LR from exception frame
	MOVD	$16, R15
el0_print_lr_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_lr_digit
	ADD	$('A'-10), R11
	B	el0_lr_char
el0_lr_digit:
	ADD	$'0', R11
el0_lr_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_lr_loop

	// Print " SP0="
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'S', R11
	MOVB	R11, (R12)
	MOVD	$'P', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)

	// Print SP_EL0 from exception frame (16 hex digits)
	MOVD	EXC_FRAME_SP_EL0(RSP), R14
	MOVD	$16, R15
el0_print_sp0_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_sp0_digit
	ADD	$('A'-10), R11
	B	el0_sp0_char
el0_sp0_digit:
	ADD	$'0', R11
el0_sp0_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_sp0_loop

	// Print " T0=" (TTBR0_EL1)
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'T', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MRS	TTBR0_EL1, R14
	MOVD	$16, R15
el0_print_ttbr0_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_ttbr0_digit
	ADD	$('A'-10), R11
	B	el0_ttbr0_char
el0_ttbr0_digit:
	ADD	$'0', R11
el0_ttbr0_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_ttbr0_loop

	// Print " TC=" (TCR_EL1)
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'T', R11
	MOVB	R11, (R12)
	MOVD	$'C', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MRS	TCR_EL1, R14
	MOVD	$16, R15
el0_print_tcr_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_tcr_digit
	ADD	$('A'-10), R11
	B	el0_tcr_char
el0_tcr_digit:
	ADD	$'0', R11
el0_tcr_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_tcr_loop

	MOVD	$'\r', R11
	MOVB	R11, (R12)
	MOVD	$'\n', R11
	MOVB	R11, (R12)

	// AT S1E0R translation check on the faulting data address (FAR), not the
	// instruction address (ELR). This checks whether EL0 can read the fault VA.
	MOVD	EXC_FRAME_FAR_ESR(RSP), R14  // R14 = FAR (faulting data address)
	// AT S1E0R, X14 = address translation, stage 1, EL0, read
	// Encoding: SYS #0, C7, C8, #2, X14 = 0xD508784E
	// op1=000, CRn=0111, CRm=1000, op2=010, Rt=01110
	WORD	$0xD508784E  // AT S1E0R, X14
	ISB	$15
	MRS	PAR_EL1, R14
	// Print "AR=" (AT Result)
	MOVD	$'A', R11
	MOVB	R11, (R12)
	MOVD	$'R', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	$16, R15
el0_print_par_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_par_digit
	ADD	$('A'-10), R11
	B	el0_par_char
el0_par_digit:
	ADD	$'0', R11
el0_par_char:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_print_par_loop
	MOVD	$' ', R11
	MOVB	R11, (R12)

	// Page table walk for the faulting VA (FAR_EL1).
	// With 4KB granule: L0[0] -> L1[0] -> L2[0] -> L3[FAR[20:12]]
	// KernelMMIOOffset = 0xFFFFFFFF00000000 to convert PA->VA

	// Get TTBR0 PA (mask out ASID in bits [63:48])
	MRS	TTBR0_EL1, R13
	AND	$0x0000FFFFFFFFFFFF, R13  // R13 = L0 table PA

	// Convert L0 PA to kernel VA
	MOVD	$0xFFFFFFFF00000000, R16
	ADD	R16, R13, R17  // R17 = L0 table VA

	// Print "PT:" then L0[0]
	MOVD	$'P', R11
	MOVB	R11, (R12)
	MOVD	$'T', R11
	MOVB	R11, (R12)
	MOVD	$':', R11
	MOVB	R11, (R12)

	// Read L0[0] (index 0 for VA 0x82F80, bits [43:39]=0)
	MOVD	(R17), R14  // R14 = L0 entry
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'0', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	R14, R8  // save L0 entry
	MOVD	$16, R15
el0_pt_l0_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_pt_l0_d
	ADD	$('A'-10), R11
	B	el0_pt_l0_c
el0_pt_l0_d:
	ADD	$'0', R11
el0_pt_l0_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_pt_l0_loop

	// Check L0 valid (bit 0)
	TST	$1, R8
	BEQ	el0_pt_invalid

	// Extract L1 table PA from L0 entry (bits [47:12])
	AND	$0x0000FFFFFFFFF000, R8, R17
	ADD	R16, R17  // R17 = L1 table VA

	// Read L1[0] (index 0 for VA 0x82F80, bits [38:30]=0)
	MOVD	(R17), R14  // R14 = L1 entry
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'1', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	R14, R8  // save L1 entry
	MOVD	$16, R15
el0_pt_l1_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_pt_l1_d
	ADD	$('A'-10), R11
	B	el0_pt_l1_c
el0_pt_l1_d:
	ADD	$'0', R11
el0_pt_l1_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_pt_l1_loop

	// Check L1 valid
	TST	$1, R8
	BEQ	el0_pt_invalid

	// Extract L2 table PA from L1 entry
	AND	$0x0000FFFFFFFFF000, R8, R17
	ADD	R16, R17  // R17 = L2 table VA

	// Read L2[0] (index 0 for VA 0x82F80, bits [29:21]=0)
	MOVD	(R17), R14  // R14 = L2 entry
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'2', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	R14, R8  // save L2 entry
	MOVD	$16, R15
el0_pt_l2_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_pt_l2_d
	ADD	$('A'-10), R11
	B	el0_pt_l2_c
el0_pt_l2_d:
	ADD	$'0', R11
el0_pt_l2_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_pt_l2_loop

	// Check L2 valid
	TST	$1, R8
	BEQ	el0_pt_invalid

	// Check if L2 is a block descriptor (bit 1 = 0) or table (bit 1 = 1)
	TST	$2, R8
	BEQ	el0_pt_block  // 2MB block, stop here

	// Extract L3 table PA from L2 entry
	AND	$0x0000FFFFFFFFF000, R8, R17
	ADD	R16, R17  // R17 = L3 table VA

	// Read L3[FAR[20:12]]: compute byte offset = ((FAR >> 12) & 0x1FF) * 8
	MOVD	EXC_FRAME_FAR_ESR(RSP), R14  // R14 = FAR
	LSR	$12, R14, R14                 // R14 = FAR >> 12 (page number)
	AND	$0x1FF, R14                   // R14 = L3 index (bits [20:12] of FAR)
	LSL	$3, R14, R14                  // R14 = byte offset (index * 8)
	ADD	R14, R17, R17                 // R17 = L3 table VA + byte offset
	MOVD	(R17), R14                    // R14 = L3 entry at correct index
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'L', R11
	MOVB	R11, (R12)
	MOVD	$'3', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	R14, R8  // save L3 entry for instruction read below
	MOVD	$16, R15
el0_pt_l3_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_pt_l3_d
	ADD	$('A'-10), R11
	B	el0_pt_l3_c
el0_pt_l3_d:
	ADD	$'0', R11
el0_pt_l3_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_pt_l3_loop

	// Read word at ELR offset within the FAR page via kernel linear map.
	// NOTE: R8 = L3 PTE for the FAR page (the data that was being accessed).
	// Using ELR's 12-bit offset in FAR's page gives useful data only when
	// ELR and FAR are in the same 4KB page; otherwise this reads unrelated data.
	// A correct IW read would require a separate L3 walk for the ELR page.
	AND	$0x0000FFFFFFFFF000, R8, R17
	// Add page offset from ELR: offset = ELR & 0xFFF
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R14
	AND	$0xFFF, R14
	ADD	R14, R17  // R17 = PA of FAR's page + ELR's page-offset
	// Convert to kernel VA via linear map
	MOVD	$0xFFFFFFFF00000000, R16
	ADD	R16, R17  // R17 = kernel VA of instruction
	// Read 32-bit instruction word
	WORD	$0xB940022E  // LDR W14, [X17] (32-bit zero-extended load)
	// Print " IW=" + 8 hex digits
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'I', R11
	MOVB	R11, (R12)
	MOVD	$'W', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MOVD	$8, R15
el0_iw_loop:
	LSR	$28, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_iw_d
	ADD	$('A'-10), R11
	B	el0_iw_c
el0_iw_d:
	ADD	$'0', R11
el0_iw_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_iw_loop

	// Print SCTLR_EL1
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'S', R11
	MOVB	R11, (R12)
	MOVD	$'C', R11
	MOVB	R11, (R12)
	MOVD	$'=', R11
	MOVB	R11, (R12)
	MRS	SCTLR_EL1, R14
	MOVD	$16, R15
el0_sc_loop:
	LSR	$60, R14, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	el0_sc_d
	ADD	$('A'-10), R11
	B	el0_sc_c
el0_sc_d:
	ADD	$'0', R11
el0_sc_c:
	MOVB	R11, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, el0_sc_loop

	B	el0_pt_done

el0_pt_block:
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'B', R11
	MOVB	R11, (R12)
	MOVD	$'K', R11
	MOVB	R11, (R12)
	B	el0_pt_done

el0_pt_invalid:
	MOVD	$' ', R11
	MOVB	R11, (R12)
	MOVD	$'I', R11
	MOVB	R11, (R12)
	MOVD	$'V', R11
	MOVB	R11, (R12)

el0_pt_done:
	MOVD	$'\r', R11
	MOVB	R11, (R12)
	MOVD	$'\n', R11
	MOVB	R11, (R12)

el0_not_svc_hang:
	B	el0_not_svc_hang

el0_return:
	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	MSR	R10, ELR_EL1
	MSR	R11, SPSR_EL1

	// Restore X28-X30
	WORD	$0xf94073fc  // ldr x28, [sp, #224]
	WORD	$0xf94077fd  // ldr x29, [sp, #232]
	MOVD	EXC_FRAME_X28+16(RSP), R10
	MOVD	R10, LR

	// Restore X8-X27
	LDP	EXC_FRAME_X8(RSP), (R8, R9)
	LDP	EXC_FRAME_X8+16(RSP), (R10, R11)
	LDP	EXC_FRAME_X8+32(RSP), (R12, R13)
	LDP	EXC_FRAME_X8+48(RSP), (R14, R15)
	LDP	EXC_FRAME_X8+64(RSP), (R16, R17)
	WORD	$0xf9404bf2  // ldr x18, [sp, #144]
	WORD	$0xf9404ff3  // ldr x19, [sp, #152]
	LDP	EXC_FRAME_X8+96(RSP), (R20, R21)
	LDP	EXC_FRAME_X8+112(RSP), (R22, R23)
	LDP	EXC_FRAME_X8+128(RSP), (R24, R25)
	LDP	EXC_FRAME_X8+144(RSP), (R26, R27)

	// Restore X0-X7
	LDP	EXC_FRAME_X0(RSP), (R0, R1)
	LDP	EXC_FRAME_X0+16(RSP), (R2, R3)
	LDP	EXC_FRAME_X0+32(RSP), (R4, R5)
	LDP	EXC_FRAME_X0+48(RSP), (R6, R7)

	// Clean up stack and return to userspace
	ADD	$EXC_FRAME_SIZE, RSP
	ERET

// ============================================================================
// el0_irq_handler - IRQ from EL0 (userspace)
// ============================================================================
// For now, just forward to the regular IRQ handler
// TODO: May need different handling for userspace context
el0_irq_handler:
	B	irq_exception_handler

// ============================================================================
// GetExceptionVectorBase - Returns address of exception vector table
// ============================================================================
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	MOVD	$·ExceptionVectorTable(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// sigreturnTrampoline — issues SYS_rt_sigreturn to kmazarin
// ============================================================================
// Called when sigtramp returns (via LR). Issues rt_sigreturn so kmazarin
// can restore the (possibly modified) register context from the ucontext.
TEXT ·sigreturnTrampoline(SB), NOSPLIT|NOFRAME, $0
	MOVD	$139, R8            // SYS_rt_sigreturn on ARM64
	SVC
	// Should not return — if it does, halt
	MOVD	$0xFFFFFFFF09000000, R0
	MOVW	$'!', R1
	MOVW	R1, (R0)
	B	-1(PC)

// getSigreturnTrampolineAddr — returns address of sigreturnTrampoline
TEXT ·getSigreturnTrampolineAddr(SB), NOSPLIT, $0-8
	MOVD	$·sigreturnTrampoline(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// GetGRegister - Returns the value of the g register (x28)
// ============================================================================
TEXT ·GetGRegister(SB), NOSPLIT, $0-8
	// Read x28 (g register) using raw instruction
	// movd x28, x0 = 0xaa1c03e0
	WORD	$0xaa1c03e0  // mov x0, x28
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// GetPC - Returns the current PC value
// ============================================================================
TEXT ·GetPC(SB), NOSPLIT, $0-8
	// ADR X0, PC+0 - get current PC into X0
	// adr x0, . = 0x10000000
	WORD	$0x10000000  // adr x0, .
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// SetVBAR - Sets VBAR_EL1 to the exception vector table address
// ============================================================================
TEXT ·SetVBAR(SB), NOSPLIT, $0-8
	MOVD	addr+0(FP), R0
	MSR	R0, VBAR_EL1
	ISB	$15		// Synchronize context
	RET

// ============================================================================
// ReadVBAR - Reads the current VBAR_EL1 value
// ============================================================================
TEXT ·ReadVBAR(SB), NOSPLIT, $0-8
	MRS	VBAR_EL1, R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// EnableIRQs - Enable interrupts by clearing the I bit in DAIF
// ============================================================================
TEXT ·EnableIRQs(SB), NOSPLIT, $0
	// MSR DAIFCLR, #2 - Clear I bit (bit 1) = enable IRQs
	// Encoded as: 0xD50342FF
	WORD	$0xD50342FF
	ISB	$15		// Synchronize context
	RET

// ============================================================================
// DisableIRQs - Disable interrupts by setting the I bit in DAIF
// ============================================================================
TEXT ·DisableIRQs(SB), NOSPLIT, $0
	// MSR DAIFSET, #2 - Set I bit (bit 1) = disable IRQs
	// Encoded as: 0xD50342DF
	WORD	$0xD50342DF
	ISB	$15		// Synchronize context
	RET

// ============================================================================
// SaveAndDisableIRQs - Save DAIF and disable IRQs atomically
// Returns: saved DAIF value in R0
// ============================================================================
TEXT ·SaveAndDisableIRQs(SB), NOSPLIT, $0-8
	// MRS X0, DAIF - Read current DAIF into R0
	// Encoded as: 0xD53B4220
	WORD	$0xD53B4200
	// MSR DAIFSET, #2 - Set I bit = disable IRQs
	WORD	$0xD50342DF
	ISB	$15
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// RestoreIRQs - Restore DAIF to a previously saved value
// Input: savedDAIF in first argument
// ============================================================================
TEXT ·RestoreIRQs(SB), NOSPLIT, $0-8
	MOVD	savedDAIF+0(FP), R0
	// MSR DAIF, X0 - Write R0 to DAIF
	// Encoded as: 0xD51B4200
	WORD	$0xD51B4200
	ISB	$15
	RET
