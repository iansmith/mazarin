//go:build !test_stubs


// exceptions_arm64.s - Kmazarin exception handlers (Go/Plan9 assembly)
//
// This file provides the exception vector table and handlers for kmazarin.
// Cardinal sets VBAR_EL1 to point to this vector before jumping to kmazarin.
//
// CRITICAL: Exception vector MUST be 2KB aligned (ARM64 requirement)

#include "textflag.h"
#include "../../docs/abi/go_abi_macros_arm64.h"

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
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R0, R1)  // Load ELR and SPSR from frame
	GO_CALL_1_0(·SetSyscallELR, R0)        // SetSyscallELR(elr)
	GO_CALL_1_0(·SetSyscallSPSR, R1)       // SetSyscallSPSR(spsr)

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

	// Copy ThreadContext to exception frame, then sync_return will restore it
	// ThreadContext: X[31], SP, ELR, SPSR
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
	// DEBUG: Print ELR low bits before storing
	MOVD	R0, R10   // Save ELR in R10
	MOVD	R1, R11   // Save SPSR in R11
	MOVD	$UART_BASE, R12
	MOVD	$'E', R13
	MOVB	R13, (R12)
	MOVD	$'L', R13
	MOVB	R13, (R12)
	MOVD	$'R', R13
	MOVB	R13, (R12)
	MOVD	$'=', R13
	MOVB	R13, (R12)
	// Print R10 (ELR) full 16 nibbles
	MOVD	R10, R14
	MOVD	$16, R15
print_elr_ctxsw:
	LSR	$60, R14, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	print_elr_ctxsw_d
	ADD	$('A'-10), R13
	B	print_elr_ctxsw_c
print_elr_ctxsw_d:
	ADD	$'0', R13
print_elr_ctxsw_c:
	MOVB	R13, (R12)
	LSL	$4, R14
	SUB	$1, R15
	CBNZ	R15, print_elr_ctxsw
	// Reload ELR/SPSR
	MOVD	R10, R0
	MOVD	R11, R1
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

	// Print " x28="
	MOVD	$' ', R11
	MOVB	R11, (R10)
	MOVD	$'x', R11
	MOVB	R11, (R10)
	MOVD	$'2', R11
	MOVB	R11, (R10)
	MOVD	$'8', R11
	MOVB	R11, (R10)
	MOVD	$'=', R11
	MOVB	R11, (R10)

	// Read x28 from saved context (at [sp, #224])
	MOVD	EXC_FRAME_X28(RSP), R12
	MOVD	$16, R13		// Counter for 16 hex digits
print_x28_data_abort:
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	print_x28_digit_da
	ADD	$('A'-10), R11
	B	print_x28_char_da
print_x28_digit_da:
	ADD	$'0', R11
print_x28_char_da:
	MOVB	R11, (R10)
	LSL	$4, R12
	SUB	$1, R13
	CBNZ	R13, print_x28_data_abort

	MOVD	$'\r', R11
	MOVB	R11, (R10)
	MOVD	$'\n', R11
	MOVB	R11, (R10)
data_abort_hang:
	B	data_abort_hang

sync_return:
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

	// DEBUG: Print ELR_EL1 and SPSR_EL1 right before ERET
	// Print "F=" prefix for ELR
	MOVD	$UART_BASE, R0
	MOVD	$'F', R1
	MOVB	R1, (R0)
	MOVD	$'=', R1
	MOVB	R1, (R0)
	// MRS ELR_EL1, X2 - read back what we wrote
	WORD	$0xD5384022	// mrs x2, elr_el1
	// Print 16 hex nibbles of ELR_EL1
	MOVD	$16, R3
	MOVD	R2, R4		// Copy to R4 for shifting
print_final_elr:
	LSR	$60, R4, R5	// Get top nibble
	CMP	$10, R5
	BLT	print_final_elr_digit
	ADD	$('A'-10), R5
	B	print_final_elr_out
print_final_elr_digit:
	ADD	$'0', R5
print_final_elr_out:
	MOVB	R5, (R0)
	LSL	$4, R4		// Shift left for next nibble
	SUB	$1, R3
	CBNZ	R3, print_final_elr
	// Print newline
	MOVD	$'\r', R1
	MOVB	R1, (R0)
	MOVD	$'\n', R1
	MOVB	R1, (R0)

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

	// DEBUG: Output '!' to show we entered IRQ handler at all
	MOVD	$UART_BASE, R11
	MOVD	$'!', R12
	MOVB	R12, (R11)

	// Read interrupt number from GIC CPU interface
	// IAR = GICC_BASE + 0x0C
	MOVD	$(GIC_CPU_BASE + GICC_IAR), R10
	MOVW	(R10), R0  // R0 = interrupt number

	// Store IAR value for later EOIR write
	MOVD	R0, R19  // Save full IAR value in R19

	// DEBUG: Output 'H' breadcrumb after count 5 to show we entered IRQ handler
	MOVD	mazzy∕kmazarin∕kirq·TimerIRQCount(SB), R10
	CMP	$5, R10
	BLT	skip_handler_breadcrumb
	MOVD	$UART_BASE, R11
	MOVD	$'H', R12
	MOVB	R12, (R11)
skip_handler_breadcrumb:

	// Mask to get interrupt ID (bits 0-9, max 1020 for GICv2)
	AND	$0x3FF, R0  // R0 = IRQ number (0-1019)

	// Check if this is the timer IRQ (27)
	CMP	$27, R0
	BNE	irq_not_timer

	// DEBUG: Output 'I' breadcrumb to show IRQ 27 was delivered
	// Check if count >= 5 to reduce noise
	MOVD	mazzy∕kmazarin∕kirq·TimerIRQCount(SB), R10
	CMP	$5, R10
	BLT	skip_irq_breadcrumb
	MOVD	$UART_BASE, R11
	MOVD	$'I', R12
	MOVB	R12, (R11)
skip_irq_breadcrumb:

	// ========================================================================
	// Timer IRQ (27) - Call pure assembly handler
	// ========================================================================
	// The timer handler sets g.preempt and g.stackguard0 directly without
	// calling any Go functions. This is safe because we're not using the
	// Go runtime at all - just setting memory locations.
	//
	// R28 contains g (saved earlier in exception frame)
	// The handler will read g from R28 and set preemption flags.
	// It also sets NeedsAsyncPreempt if threshold exceeded.
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
	// Thread preemption check - switch to another thread if threshold exceeded
	// ========================================================================
	// Check NeedsThreadPreempt flag set by TimerIRQHandlerAsm
	MOVW	mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB), R10

	// DEBUG: Print NeedsThreadPreempt value after first 10 timer IRQs
	// This will show 'N' followed by '0' or '1' indicating the flag value
	MOVD	mazzy∕kmazarin∕kirq·TimerIRQCount(SB), R11
	CMP	$10, R11
	BLT	skip_needspreempt_debug
	MOVD	$UART_BASE, R11
	MOVD	$'N', R12
	MOVB	R12, (R11)
	// Print value as hex digit ('0' or '1')
	ADD	$'0', R10, R12
	MOVB	R12, (R11)
skip_needspreempt_debug:

	CBZ	R10, timer_no_thread_preempt

	// Clear NeedsThreadPreempt flag
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB)

	// CRITICAL: Switch to kmazarin's g before calling Go code
	// The timer may have interrupted userspace (priest) which has a different g
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, timer_no_thread_preempt  // Skip if not initialized
	WORD	$0xaa0a03fc  // mov x28, x10

	// Call CheckThreadPreemption(framePtr) to check and perform switch
	// func CheckThreadPreemption(framePtr uint64) uint64
	MOVD	RSP, R0                        // R0 = framePtr (exception frame)
	GO_CALL_1_1(·CheckThreadPreemption, R0)
	MOVD	R0, R21                        // R21 = new context pointer (or 0)

	// Check if context switch happened
	CBZ	R21, timer_no_thread_preempt

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

	// DEBUG: Print ELR being used for context switch
	// Save R0-R3 temporarily
	SUB	$32, RSP
	STP	(R2, R3), 0(RSP)
	STP	(R4, R5), 16(RSP)
	// R0 already has ELR, save it to R4
	MOVD	R0, R4
	// Print "S="
	MOVD	$UART_BASE, R2
	MOVD	$'S', R3
	MOVB	R3, (R2)
	MOVD	$'=', R3
	MOVB	R3, (R2)
	// Print last 4 hex digits of ELR (lower 16 bits)
	MOVD	$4, R5  // 4 hex digits
print_switch_elr:
	LSR	$12, R4, R3
	AND	$0xF, R3
	CMP	$10, R3
	BLT	print_switch_elr_digit
	ADD	$('A'-10), R3
	B	print_switch_elr_char
print_switch_elr_digit:
	ADD	$'0', R3
print_switch_elr_char:
	MOVB	R3, (R2)
	LSL	$4, R4
	SUB	$1, R5
	CBNZ	R5, print_switch_elr
	// Restore R2-R5
	LDP	0(RSP), (R2, R3)
	LDP	16(RSP), (R4, R5)
	ADD	$32, RSP

	// Skip async preemption - we already switched threads
	B	timer_no_preempt

timer_no_thread_preempt:
	// ========================================================================
	// Async preemption injection (pure assembly, no Go calls)
	// ========================================================================
	// Check if assembly handler requested async preemption.
	// This is set by TimerIRQHandlerAsm when threshold exceeded.
	MOVW	mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB), R10
	CBZ	R10, timer_no_preempt_no_flag

	// Check if runtime is ready for async preemption
	MOVW	mazzy∕kmazarin∕kirq·ReadyForAsyncPreempt(SB), R10
	CBZ	R10, timer_no_preempt_not_ready

	// ========================================================================
	// CRITICAL: Only inject asyncPreempt if we came from EL0 (userspace)
	// ========================================================================
	// When a syscall (SVC) is in progress and a timer IRQ fires:
	// - The goroutine is in _Gsyscall state (atomicstatus=3)
	// - Injecting asyncPreempt would cause "bad g status" panic
	// - SPSR.M[3:0] tells us which EL we came from:
	//   0b0000 (0) = EL0t, 0b0100 (4) = EL1t, 0b0101 (5) = EL1h
	// Only inject if M[3:0] == 0 (came from EL0)
	MOVD	EXC_FRAME_ELR_SPSR+8(RSP), R10  // R10 = SPSR
	AND	$0xF, R10  // R10 = M[3:0] bits
	CBNZ	R10, timer_no_preempt_in_kernel  // If not EL0, skip

	// ========================================================================
	// CRITICAL: Check if running on g0 (scheduler goroutine)
	// ========================================================================
	// Never inject async preemption on g0 - the Go runtime will crash with
	// "mcall called on m->g0 stack" if we try to preempt the scheduler.
	// g0 is the scheduler goroutine for each M (machine/OS thread).
	//
	// IMPORTANT: We must use the ORIGINAL g from the exception frame, not R28!
	// R28 was switched to kmazarin's g0 after calling TimerIRQHandlerAsm.
	// The original userspace g is saved in the exception frame at EXC_FRAME_X28.

	MOVD	EXC_FRAME_X28(RSP), R10  // R10 = original g (from exception frame)
	CBZ	R10, timer_no_preempt  // No g, skip

	// For the g0 check, we need to check if the ORIGINAL g is m.g0.
	// But we need to be careful: the original g might be a userspace g pointer,
	// which we can't safely dereference from kernel context without proper checks.
	//
	// First, check if g is in kernel memory (high 16 bits == 0xFFFF)
	// If it's in userspace memory, it's definitely not kmazarin's g0
	LSR	$48, R10, R11
	MOVD	$0xFFFF, R12
	CMP	R11, R12
	BNE	timer_g0_check_done  // g in userspace, definitely not g0

	// g is in kernel memory - check if it's g0
	// Load g.m offset and get m pointer
	MOVD	mazzy∕kmazarin∕kirq·PreemptGMOffset(SB), R11
	ADD	R10, R11, R11  // R11 = &g.m
	MOVD	(R11), R11     // R11 = g.m (m pointer)
	CBZ	R11, timer_no_preempt  // No m, skip

	// Load m.g0 (offset 0 in m struct)
	MOVD	(R11), R12     // R12 = m.g0

	// Compare current g with m.g0
	CMP	R10, R12
	BEQ	timer_no_preempt_on_g0  // If g == m.g0, skip preemption

timer_g0_check_done:
	// ========================================================================
	// CRITICAL: Check g.atomicstatus == _Grunning before preemption
	// ========================================================================
	// The Go runtime sets g.atomicstatus = _Gsyscall BEFORE the actual SVC
	// instruction, while still in userspace. If we preempt at that moment,
	// SPSR shows EL0 (passes the EL check), but asyncPreempt will fail with
	// "bad g status" because the goroutine is marked as _Gsyscall.
	//
	// IMPORTANT: We must use the ORIGINAL g from the exception frame, not R28!
	// R28 was switched to kmazarin's g0 for calling Go code (line ~793).
	// The original userspace g is saved in the exception frame at EXC_FRAME_X28.
	MOVD	EXC_FRAME_X28(RSP), R4  // R4 = original g (from userspace)

	// Load g.atomicstatus offset
	MOVD	mazzy∕kmazarin∕kirq·PreemptGStatusOffset(SB), R5
	ADD	R4, R5  // R5 = &g.atomicstatus

	// Load status (32-bit atomic)
	// Note: Using regular load - userspace memory should be accessible from EL1
	// since PAN is not enabled in our configuration
	MOVW	(R5), R6  // R6 = g.atomicstatus

	// Mask off _Gscan bit (0x1000) when comparing
	MOVW	mazzy∕kmazarin∕kirq·PreemptGScan(SB), R7
	BIC	R7, R6, R8  // R8 = status & ~_Gscan

	// Compare with _Grunning (must be exactly running, not syscall/waiting/etc)
	MOVW	mazzy∕kmazarin∕kirq·PreemptGRunning(SB), R7
	CMP	R8, R7
	BNE	timer_no_preempt_wrong_status  // Not running, skip preemption

	// ========================================================================
	// CRITICAL: Check m.locks == 0 before preemption
	// ========================================================================
	// The Go runtime's canPreemptM() checks mp.locks == 0.
	// If the goroutine is holding locks (e.g., during fmt.Print which uses
	// mutexes), calling asyncPreempt will trigger "schedule: holding locks".
	//
	// R4 still contains the g pointer from above.
	// Load g.m to get the m pointer
	MOVD	mazzy∕kmazarin∕kirq·PreemptGMOffset(SB), R5
	ADD	R4, R5  // R5 = &g.m
	MOVD	(R5), R6  // R6 = g.m (pointer to m struct)
	CBZ	R6, timer_no_preempt  // If m is nil, skip

	// Load m.locks
	MOVD	mazzy∕kmazarin∕kirq·PreemptMLocksOffset(SB), R7
	ADD	R6, R7  // R7 = &m.locks
	MOVW	(R7), R8  // R8 = m.locks (int32)

	// If m.locks != 0, skip preemption (goroutine is holding locks)
	CBNZ	R8, timer_no_preempt_holding_locks

	// UNIFIED ASYNCPREEMPT: Use per-thread asyncPreempt address
	// This supports both kmazarin goroutines and priest goroutines:
	// - Kmazarin threads: AsyncPreemptAddr = kmazarin's asyncPreemptWrapper
	// - Priest threads: AsyncPreemptAddr = priest's registered asyncPreempt
	//
	// Get current thread's AsyncPreemptAddr using runtime-verified offset
	// (ThreadAsyncPreemptAddrOffset is computed via unsafe.Offsetof at init)
	MOVD	main·CurrentThread(SB), R10  // R10 = *Thread
	CBZ	R10, timer_no_preempt  // No current thread

	// CRITICAL: Check InCloneSetup flag - skip async preempt for clone children
	// Clone children have fn/gp/mp stored on stack that would be overwritten by
	// the async preempt LR/R29 push. Flag is cleared on first syscall.
	MOVD	main·ThreadInCloneSetupOffset(SB), R11  // R11 = InCloneSetup offset
	ADD	R11, R10, R11  // R11 = &thread.InCloneSetup
	MOVW	(R11), R12  // R12 = InCloneSetup (uint32)
	CBNZ	R12, timer_no_preempt_in_clone_setup

	// Re-load CurrentThread since R10 was used for offset calculation
	MOVD	main·CurrentThread(SB), R10  // R10 = *Thread
	MOVD	main·ThreadAsyncPreemptAddrOffset(SB), R11  // R11 = AsyncPreemptAddr offset
	ADD	R11, R10, R11  // R11 = &thread.AsyncPreemptAddr
	MOVD	(R11), R10  // R10 = thread.AsyncPreemptAddr

	// CRITICAL: Check if asyncPreempt address is zero (not yet initialized)
	// Skip preemption if address is not set
	CBZ	R10, timer_no_preempt

	// Validate asyncPreempt address is 4-byte aligned
	TST	$3, R10
	BNE	timer_no_preempt_wrapper_misaligned

	// Get original ELR from exception frame
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R11

	// Validate ELR is 4-byte aligned
	TST	$3, R11
	BNE	timer_no_preempt_elr_misaligned

	// Check we're not already in asyncPreempt (avoid nested preemption)
	// asyncPreempt is ~100 bytes, check if ELR is within [asyncPreempt, asyncPreempt+256)
	SUB	R10, R11, R12  // R12 = ELR - asyncPreemptAddr
	CMP	$256, R12
	BLO	timer_no_preempt_already_in_async  // If ELR within 256 bytes of asyncPreempt, skip

	// Clear NeedsAsyncPreempt flag
	MOVW	$0, R12
	MOVW	R12, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)

	// ========================================================================
	// PUSH ORIGINAL LR AND R29 TO USER STACK (like Go runtime's pushCall)
	// ========================================================================
	// Go's asyncPreempt expects the stack to be set up like pushCall() does:
	//   (SP+0) = original LR (so asyncPreempt can restore it before returning)
	//   (SP+8) = original R29 (frame pointer for debugger compatibility)
	//
	// When asyncPreempt finishes, it does:
	//   MOVD 496(RSP), R30  // Restore original LR from (SP+496) after its frame
	//   MOVD (RSP), R27     // Load interrupted PC for return
	//   RET (R27)           // Return to interrupted instruction
	//
	// Without this, asyncPreempt reads garbage for the original LR, causing
	// corruption when the interrupted function eventually returns.

	// R10 = asyncPreempt address (from earlier)
	// R11 = original ELR (interrupted PC, from earlier)

	// Get original LR (X30) and R29 from exception frame
	MOVD	EXC_FRAME_X28+16(RSP), R12      // R12 = original LR (X30)
	MOVD	EXC_FRAME_X28+8(RSP), R13       // R13 = original R29 (frame pointer)

	// DEBUG: Print original LR value we're about to store
	// Save R0-R3 on stack temporarily
	SUB	$32, RSP
	STP	(R0, R1), 0(RSP)
	STP	(R2, R3), 16(RSP)
	// Print "[LR:"
	MOVD	$UART_BASE, R0
	MOVD	$'[', R1
	MOVB	R1, (R0)
	MOVD	$'L', R1
	MOVB	R1, (R0)
	MOVD	$'R', R1
	MOVB	R1, (R0)
	MOVD	$':', R1
	MOVB	R1, (R0)
	// Print R12 (original LR) as 16 hex digits
	MOVD	R12, R2
	MOVD	$16, R3
print_orig_lr:
	LSR	$60, R2, R1
	CMP	$10, R1
	BLT	print_orig_lr_digit
	ADD	$('A'-10), R1
	B	print_orig_lr_out
print_orig_lr_digit:
	ADD	$'0', R1
print_orig_lr_out:
	MOVB	R1, (R0)
	LSL	$4, R2
	SUB	$1, R3
	CBNZ	R3, print_orig_lr
	// Print "]"
	MOVD	$']', R1
	MOVB	R1, (R0)
	// Restore R0-R3
	LDP	0(RSP), (R0, R1)
	LDP	16(RSP), (R2, R3)
	ADD	$32, RSP

	// Get user SP and decrease by 16 (like pushCall does)
	MOVD	EXC_FRAME_SP_EL0(RSP), R14      // R14 = original user SP
	SUB	$16, R14                        // R14 = new SP (allocate 16 bytes)

	// Store original LR and R29 to user stack using STTR (unprivileged store)
	// This works even if PAN (Privileged Access Never) is enabled
	// STTR X12, [X14, #0]  - Store original LR at new_sp
	// STTR X13, [X14, #8]  - Store original R29 at new_sp+8
	WORD	$0xF80009CC                     // sttr x12, [x14]
	WORD	$0xF80089CD                     // sttr x13, [x14, #8]

	// Set up preemption return values
	// R20 = NewELR (asyncPreempt address)
	// R21 = NewSP (decreased by 16 for the pushCall-style frame)
	// R22 = NewLR (original ELR, so asyncPreempt knows where to return)
	// R23 = DoPreempt (1 = true)
	MOVD	R10, R20                        // NewELR = asyncPreemptAddr
	MOVD	R14, R21                        // NewSP = adjusted SP (decreased by 16)
	MOVD	R11, R22                        // NewLR = original ELR (interrupted PC)
	MOVD	$1, R23                         // DoPreempt = true

	B	irq_write_eoir

timer_no_preempt_no_flag:
	// NeedsAsyncPreempt not set - cooperative preemption will handle it
	B	timer_no_preempt

timer_no_preempt_not_ready:
	// DEBUG: Runtime not ready - print "!R!"
	MOVD	$(UART_BASE), R10
	MOVD	$'!', R11
	MOVB	R11, (R10)
	MOVD	$'R', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)
	B	timer_no_preempt

timer_no_preempt_on_g0:
	// Running on g0 (scheduler) - skip async preemption
	// Just clear the flag and return - scheduler will handle it
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)
	B	timer_no_preempt

timer_no_preempt_in_kernel:
	// Timer fired while in EL1 (kernel) - likely handling a syscall
	// Clear the flag - we'll try again next tick when thread returns to EL0
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)
	B	timer_no_preempt

timer_no_preempt_wrong_status:
	// Goroutine is not in _Grunning state (likely _Gsyscall)
	// This happens when Go runtime sets g.atomicstatus = _Gsyscall in userspace
	// just BEFORE the actual SVC instruction. Skip asyncPreempt injection.
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)
	B	timer_no_preempt

timer_no_preempt_holding_locks:
	// Goroutine is holding locks (m.locks != 0)
	// Preempting now would cause "schedule: holding locks" panic.
	// Skip and try again next tick.
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)
	B	timer_no_preempt

timer_no_preempt_in_clone_setup:
	// Thread is a clone child still reading fn/gp/mp from stack.
	// Async preempt would overwrite these values with LR/R29.
	// Clear flag and skip - InCloneSetup will be cleared on first syscall.
	MOVW	$0, R10
	MOVW	R10, mazzy∕kmazarin∕kirq·NeedsAsyncPreempt(SB)
	B	timer_no_preempt

// timer_no_preempt_userspace: REMOVED
// Unified asyncPreempt injection now handles both kmazarin and priest threads
// by using the per-thread AsyncPreemptAddr field.

timer_no_preempt_wrapper_misaligned:
	// DEBUG: Wrapper misaligned - print "!W!"
	MOVD	$(UART_BASE), R10
	MOVD	$'!', R11
	MOVB	R11, (R10)
	MOVD	$'W', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)
	B	timer_no_preempt

timer_no_preempt_elr_misaligned:
	// DEBUG: ELR misaligned - print "!E!" to distinguish from other 'E's
	MOVD	$(UART_BASE), R10
	MOVD	$'!', R11
	MOVB	R11, (R10)
	MOVD	$'E', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)
	B	timer_no_preempt

timer_no_preempt_already_in_async:
	// Already in asyncPreempt - skip preemption
	B	timer_no_preempt

timer_no_preempt:
	// No preemption - cooperative preemption via stackguard0 is still active
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23

	B	irq_write_eoir

irq_not_timer:
	// ========================================================================
	// Non-timer IRQs - Set pending flag for bottom-half processing
	// ========================================================================
	// R0 contains the masked IRQ number
	//
	// We can't call Go code from IRQ context (wrong stack, wrong g, etc.)
	// Instead, set a flag that the event poller will check, then the
	// bottom-half processor will call the registered handler in safe Go context.
	//
	// This is the same pattern used for UART RX/TX and timer deadlines.

	// DEBUG: If timer count >= 640, output non-timer IRQ number to see what we're getting
	MOVD	mazzy∕kmazarin∕kirq·TimerIRQCount(SB), R10
	CMP	$640, R10
	BLT	skip_nontimer_debug
	// Output '!' to show non-timer IRQ
	MOVD	$UART_BASE, R11
	MOVD	$'!', R12
	MOVB	R12, (R11)
	// Output IRQ number as 2 hex digits
	MOVD	R0, R13  // Save R0
	LSR	$4, R0, R12
	AND	$0xF, R12
	CMP	$10, R12
	BLT	nontimer_digit1
	ADD	$('A'-10), R12
	B	nontimer_char1
nontimer_digit1:
	ADD	$'0', R12
nontimer_char1:
	MOVB	R12, (R11)
	MOVD	R13, R12  // Restore
	AND	$0xF, R12
	CMP	$10, R12
	BLT	nontimer_digit2
	ADD	$('A'-10), R12
	B	nontimer_char2
nontimer_digit2:
	ADD	$'0', R12
nontimer_char2:
	MOVB	R12, (R11)
	MOVD	$' ', R12
	MOVB	R12, (R11)
	MOVD	R13, R0  // Restore R0
skip_nontimer_debug:

	// Check if IRQ number is in valid range (0-1019)
	CMP	$1020, R0
	BGE	irq_invalid

	// Set pending flag: irqPendingFlags[irqNum] = 1
	// Calculate address: &irqPendingFlags[0] + (irqNum * 4)
	MOVD	$·irqPendingFlags(SB), R10
	MOVD	$4, R11           // sizeof(uint32)
	MUL	R11, R0, R11      // R11 = irqNum * 4
	ADD	R11, R10          // R10 = &irqPendingFlags[irqNum]
	MOVD	$1, R12
	MOVW	R12, (R10)        // Store 1 to flag

	// Store IAR value: irqIARValues[irqNum] = IAR (for EOIR write in bottom-half)
	MOVD	$·irqIARValues(SB), R10
	// R11 still contains irqNum * 4
	ADD	R11, R10          // R10 = &irqIARValues[irqNum]
	MOVW	R19, (R10)        // Store IAR value (R19 saved earlier)

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

	// Check if we should modify frame for preemption
	CMP	$0, R23  // doPreempt == true?
	BEQ	irq_return  // No preemption, restore normally

	// Preemption requested - modify exception frame for call injection
	// Update ELR_EL1 to point to asyncPreempt
	MOVD	R20, R10
	MOVD	R10, EXC_FRAME_ELR_SPSR(RSP)  // Update ELR in frame

	// Update SP_EL0 to adjusted stack
	MOVD	R21, EXC_FRAME_SP_EL0(RSP)

	// Update LR to interrupted PC (so asyncPreempt can return)
	MOVD	R22, EXC_FRAME_X28+16(RSP)  // Update saved LR in frame

	// Fall through to irq_return

irq_return:
	// NOTE: Do NOT write GICC_EOIR here for level-triggered interrupts!
	// For level-triggered interrupts (like UART TX), the interrupt condition
	// may still be true (e.g., TX FIFO has space). Writing EOIR now would
	// cause the GIC to immediately re-fire the interrupt, creating a storm.
	// Instead, the bottom-half writes EOIR after clearing the interrupt condition.

	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R10, R11)
	// CRITICAL: Force IRQs enabled in SPSR by clearing DAIF.I bit (bit 7 = 0x80)
	// This ensures IRQs are enabled after ERET, preventing stuck-disabled-IRQ chains
	BIC	$0x80, R11, R11
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

	// NOTE: 'U' debug print disabled to test if it affects timing fairness
	// MOVD	$UART_BASE, R12
	// MOVD	$'U', R11
	// MOVB	R11, (R12)

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
	// NOTE: 'K' debug print disabled to test if it affects timing fairness
	// MOVD	$UART_BASE, R11
	// MOVD	$'K', R12
	// MOVB	R12, (R11)

	// SVC from userspace - first save ELR and SPSR for clone
	// Without this, clone would use stale values from a previous EL1 syscall!
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R0, R1)  // Load ELR and SPSR from frame
	GO_CALL_1_0(·SetSyscallELR, R0)        // SetSyscallELR(elr)
	GO_CALL_1_0(·SetSyscallSPSR, R1)       // SetSyscallSPSR(spsr)

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

	// Call syscall dispatcher
	GO_CALL_7_1(·SyscallDispatch, R0, R2, R3, R4, R5, R6, R7)

	// Store return value back to X0 in exception frame
	MOVD	R0, EXC_FRAME_X0(RSP)

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

	// Copy ThreadContext to exception frame, then el0_return will restore it
	// ThreadContext: X[31], SP, ELR, SPSR
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

	// DEBUG: Print ELR low byte before storing
	MOVD	R0, R10   // Save ELR temporarily
	MOVD	R1, R11   // Save SPSR temporarily
	MOVD	$UART_BASE, R12
	MOVD	$'E', R13
	MOVB	R13, (R12)
	MOVD	$'=', R13
	MOVB	R13, (R12)
	// Print R10 (ELR) low 4 nibbles
	LSR	$12, R10, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	el0_elr_d1
	ADD	$('A'-10), R13
	B	el0_elr_c1
el0_elr_d1:
	ADD	$'0', R13
el0_elr_c1:
	MOVB	R13, (R12)
	LSR	$8, R10, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	el0_elr_d2
	ADD	$('A'-10), R13
	B	el0_elr_c2
el0_elr_d2:
	ADD	$'0', R13
el0_elr_c2:
	MOVB	R13, (R12)
	LSR	$4, R10, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	el0_elr_d3
	ADD	$('A'-10), R13
	B	el0_elr_c3
el0_elr_d3:
	ADD	$'0', R13
el0_elr_c3:
	MOVB	R13, (R12)
	AND	$0xF, R10, R13
	CMP	$10, R13
	BLT	el0_elr_d4
	ADD	$('A'-10), R13
	B	el0_elr_c4
el0_elr_d4:
	ADD	$'0', R13
el0_elr_c4:
	MOVB	R13, (R12)

	// Restore and store
	MOVD	R10, R0   // Restore ELR (original was clobbered by shifts)
	// Actually we shifted R10, need to reload from ThreadContext
	LDP	256(R21), (R0, R1)
	STP	(R0, R1), EXC_FRAME_ELR_SPSR(RSP)

	B	el0_return

el0_no_switch:
	// No context switch, normal return
	B	el0_return

el0_check_data_abort:
	// Check if this is Data Abort from lower EL (EC = 0x24)
	CMP	$0x24, R10
	BNE	el0_not_svc

	// CRITICAL: Switch to kmazarin's g before calling Go code (same as SVC case)
	// Only switch if kmazarinG0Addr is non-zero (initialized).
	MOVD	·kmazarinG0Addr(SB), R10
	CBZ	R10, skip_g_switch_el0_da
	WORD	$0xaa0a03fc  // mov x28, x10
skip_g_switch_el0_da:

	// Data Abort from userspace - try to handle page fault
	// Get fault address from FAR_EL1 (already in exception frame)
	MOVD	EXC_FRAME_FAR_ESR(RSP), R19

	// Call HandleUserPageFaultAsm(faultAddr) to try to handle the page fault
	// func HandleUserPageFaultAsm(faultAddr uint64) uint64 - returns 1 if handled, 0 if not
	GO_CALL_1_1(·HandleUserPageFaultAsm, R19)
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
// asyncPreemptWrapper - Full register-saving wrapper for async preemption
// ============================================================================
// runtime.asyncPreempt has a stack management bug (allocates 0x1f0, deallocates
// 0x200), causing a 16-byte stack leak per preemption. This wrapper properly
// saves ALL registers and has matched stack allocation/deallocation.
//
// When called (via ERET from IRQ handler):
//   LR = original interrupted PC (set by IRQ handler)
//   R28 = g (restored from exception frame)
//   All other registers = goroutine's values at interruption point
//
// Frame layout (512 bytes total, 0x200):
//   [SP+0]:   LR (return address)
//   [SP+8]:   X0-X26 (216 bytes, 27 registers)
//   [SP+224]: X29 (frame pointer)
//   [SP+232]: NZCV
//   [SP+240]: FPSR
//   [SP+248]: D0-D31 (256 bytes, 32 FP registers)
//   [SP+504]: padding to 512
//
// This is a NOFRAME function - we manage the stack ourselves to ensure
// exact match between allocation and deallocation.
//
TEXT ·asyncPreemptWrapper(SB), NOSPLIT|NOFRAME, $0
	// Allocate 512 bytes for save area (must match deallocation exactly!)
	SUB	$512, RSP

	// Save LR (original interrupted PC) at [SP+0]
	MOVD	LR, (RSP)

	// Save X0-X7 at [SP+8]
	STP	(R0, R1), 8(RSP)
	STP	(R2, R3), 24(RSP)
	STP	(R4, R5), 40(RSP)
	STP	(R6, R7), 56(RSP)

	// Save X8-X15 at [SP+72]
	STP	(R8, R9), 72(RSP)
	STP	(R10, R11), 88(RSP)
	STP	(R12, R13), 104(RSP)
	STP	(R14, R15), 120(RSP)

	// Save X16-X17 at [SP+136]
	STP	(R16, R17), 136(RSP)

	// Save X19-X26 at [SP+152] (skip X18 platform register, skip X28 g register)
	// X19-X20
	WORD	$0xa9094ff3  // stp x19, x20, [sp, #144]
	// X21-X22
	WORD	$0xa90a57f5  // stp x21, x22, [sp, #160]
	// X23-X24
	WORD	$0xa90b5ff7  // stp x23, x24, [sp, #176]
	// X25-X26
	WORD	$0xa90c67f9  // stp x25, x26, [sp, #192]

	// Save X27 at [SP+208]
	WORD	$0xf9006bfb  // str x27, [sp, #208]

	// Save X29 (frame pointer) at [SP+216]
	WORD	$0xf9006ffd  // str x29, [sp, #216]

	// Save NZCV at [SP+224]
	MRS	NZCV, R0
	MOVD	R0, 224(RSP)

	// Save FPSR at [SP+232]
	// MRS FPSR, X0 = 0xD53B4420
	WORD	$0xD53B4420
	MOVD	R0, 232(RSP)

	// Save FP registers D0-D31 at [SP+240]
	// 6d0f87e0 = stp d0, d1, [sp, #248] (immediate = 248/8 = 31 = 0x1f)
	// Using offsets: 240, 256, 272, 288, 304, 320, 336, 352, 368, 384, 400, 416, 432, 448, 464, 480
	WORD	$0x6d0f87e0  // stp d0, d1, [sp, #240]
	WORD	$0x6d108fe2  // stp d2, d3, [sp, #256]
	WORD	$0x6d1197e4  // stp d4, d5, [sp, #272]
	WORD	$0x6d129fe6  // stp d6, d7, [sp, #288]
	WORD	$0x6d13a7e8  // stp d8, d9, [sp, #304]
	WORD	$0x6d14afea  // stp d10, d11, [sp, #320]
	WORD	$0x6d15b7ec  // stp d12, d13, [sp, #336]
	WORD	$0x6d16bfee  // stp d14, d15, [sp, #352]
	WORD	$0x6d17c7f0  // stp d16, d17, [sp, #368]
	WORD	$0x6d18cff2  // stp d18, d19, [sp, #384]
	WORD	$0x6d19d7f4  // stp d20, d21, [sp, #400]
	WORD	$0x6d1adff6  // stp d22, d23, [sp, #416]
	WORD	$0x6d1be7f8  // stp d24, d25, [sp, #432]
	WORD	$0x6d1ceffa  // stp d26, d27, [sp, #448]
	WORD	$0x6d1df7fc  // stp d28, d29, [sp, #464]
	WORD	$0x6d1efffe  // stp d30, d31, [sp, #480]

	// Call asyncPreempt2
	// R28 (g) is still valid (not saved, not modified)
	BL	runtime·asyncPreempt2(SB)

	// CRITICAL FIX: Re-enable IRQs after asyncPreempt2 returns
	// The Go runtime scheduler disables IRQs during scheduling and doesn't
	// re-enable them before returning. Without this fix, timer IRQs stop
	// being delivered after the first async preemption, causing priests
	// to never be scheduled.
	// Use MSR DAIFClr, #2 to clear the I bit (enable IRQs).
	// Encoding: 0xD50342FF (same as EnableIRQs)
	WORD	$0xD50342FF  // MSR DAIFClr, #2 (clear I bit, enable IRQs)
	ISB	$15

	// Restore FP registers D0-D31 from [SP+240]
	WORD	$0x6d4f87e0  // ldp d0, d1, [sp, #240]
	WORD	$0x6d508fe2  // ldp d2, d3, [sp, #256]
	WORD	$0x6d5197e4  // ldp d4, d5, [sp, #272]
	WORD	$0x6d529fe6  // ldp d6, d7, [sp, #288]
	WORD	$0x6d53a7e8  // ldp d8, d9, [sp, #304]
	WORD	$0x6d54afea  // ldp d10, d11, [sp, #320]
	WORD	$0x6d55b7ec  // ldp d12, d13, [sp, #336]
	WORD	$0x6d56bfee  // ldp d14, d15, [sp, #352]
	WORD	$0x6d57c7f0  // ldp d16, d17, [sp, #368]
	WORD	$0x6d58cff2  // ldp d18, d19, [sp, #384]
	WORD	$0x6d59d7f4  // ldp d20, d21, [sp, #400]
	WORD	$0x6d5adff6  // ldp d22, d23, [sp, #416]
	WORD	$0x6d5be7f8  // ldp d24, d25, [sp, #432]
	WORD	$0x6d5ceffa  // ldp d26, d27, [sp, #448]
	WORD	$0x6d5df7fc  // ldp d28, d29, [sp, #464]
	WORD	$0x6d5efffe  // ldp d30, d31, [sp, #480]

	// Restore FPSR from [SP+232]
	MOVD	232(RSP), R0
	// MSR FPSR, X0 = 0xD51B4420
	WORD	$0xD51B4420

	// Restore NZCV from [SP+224]
	MOVD	224(RSP), R0
	MSR	R0, NZCV

	// Restore X29 from [SP+216]
	WORD	$0xf9406ffd  // ldr x29, [sp, #216]

	// Restore X27 from [SP+208]
	WORD	$0xf9406bfb  // ldr x27, [sp, #208]

	// Restore X25-X26 from [SP+192]
	WORD	$0xa94c67f9  // ldp x25, x26, [sp, #192]

	// Restore X23-X24 from [SP+176]
	WORD	$0xa94b5ff7  // ldp x23, x24, [sp, #176]

	// Restore X21-X22 from [SP+160]
	WORD	$0xa94a57f5  // ldp x21, x22, [sp, #160]

	// Restore X19-X20 from [SP+144]
	WORD	$0xa9494ff3  // ldp x19, x20, [sp, #144]

	// Restore X16-X17 from [SP+136]
	LDP	136(RSP), (R16, R17)

	// Restore X8-X15 from [SP+72]
	LDP	72(RSP), (R8, R9)
	LDP	88(RSP), (R10, R11)
	LDP	104(RSP), (R12, R13)
	LDP	120(RSP), (R14, R15)

	// Restore X0-X7 from [SP+8]
	LDP	8(RSP), (R0, R1)
	LDP	24(RSP), (R2, R3)
	LDP	40(RSP), (R4, R5)
	LDP	56(RSP), (R6, R7)

	// Restore LR from [SP+0]
	MOVD	(RSP), LR

	// Deallocate stack (MUST match allocation exactly!)
	ADD	$512, RSP

	// Return to original interrupted PC
	RET

// ============================================================================
// GetExceptionVectorBase - Returns address of exception vector table
// ============================================================================
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	MOVD	$·ExceptionVectorTable(SB), R0
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// getAsyncPreemptWrapperAddr - Returns address of asyncPreemptWrapper
// ============================================================================
// This function returns the address of asyncPreemptWrapper so it can be stored
// in a global variable for the IRQ handler to read. This avoids issues with
// loading function addresses directly in assembly (ABI0 symbol resolution).
TEXT ·getAsyncPreemptWrapperAddr(SB), NOSPLIT, $0-8
	MOVD	$·asyncPreemptWrapper(SB), R0
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
