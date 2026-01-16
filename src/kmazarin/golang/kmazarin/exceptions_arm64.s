//go:build qemuvirt && aarch64

// exceptions_arm64.s - Kmazarin exception handlers (Go/Plan9 assembly)
//
// This file provides the exception vector table and handlers for kmazarin.
// Cardinal sets VBAR_EL1 to point to this vector before jumping to kmazarin.
//
// CRITICAL: Exception vector MUST be 2KB aligned (ARM64 requirement)

#include "textflag.h"
#include "../../../../docs/abi/go_abi_macros_arm64.h"

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
	MOVD	$UART_BASE, R10
	MOVD	$'F', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

el1_spx_serror:
	MOVD	$UART_BASE, R10
	MOVD	$'S', R11
	MOVB	R11, (R10)
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0

// ============================================================================
// Lower EL AArch64 (0x400-0x5FF) - Not used (no user space yet)
// ============================================================================
el0_aarch64_sync:
	B	unhandled_exception
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0
	WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0; WORD $0

el0_aarch64_irq:
	B	unhandled_exception
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

	// SVC: First save ELR and SPSR so clone can get child's return address and state
	// ELR and SPSR are already in the exception frame from earlier save
	LDP	EXC_FRAME_ELR_SPSR(RSP), (R0, R1)  // Load ELR and SPSR from frame
	GO_CALL_1_0(·SetSyscallELR, R0)        // SetSyscallELR(elr)
	GO_CALL_1_0(·SetSyscallSPSR, R1)       // SetSyscallSPSR(spsr)

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

	// Check if this is PC alignment fault (EC=0x22) or instruction abort (EC=0x21)
	CMP	$0x22, R20
	BEQ	print_faulting_pc
	CMP	$0x21, R20
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
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
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
	// R28 contains g (saved earlier in exception frame)
	// The handler will read g from R28 and set preemption flags.
	// It also sets NeedsAsyncPreempt if threshold exceeded.
	CALL	kmazarin∕kirq·TimerIRQHandlerAsm(SB)

	// ========================================================================
	// Async preemption injection (pure assembly, no Go calls)
	// ========================================================================
	// Check if assembly handler requested async preemption.
	// This is set by TimerIRQHandlerAsm when threshold exceeded.
	MOVW	kmazarin∕kirq·NeedsAsyncPreempt(SB), R10
	CBZ	R10, timer_no_preempt

	// Check if runtime is ready for async preemption
	MOVW	kmazarin∕kirq·ReadyForAsyncPreempt(SB), R10
	CBZ	R10, timer_no_preempt

	// Get asyncPreemptWrapper address (our wrapper that saves g)
	MOVD	$·asyncPreemptWrapper(SB), R10

	// Validate wrapper address is 4-byte aligned
	TST	$3, R10
	BNE	timer_no_preempt

	// Get original ELR from exception frame
	MOVD	EXC_FRAME_ELR_SPSR(RSP), R11

	// Validate ELR is 4-byte aligned
	TST	$3, R11
	BNE	timer_no_preempt

	// Check we're not already in asyncPreempt (avoid nested preemption)
	// asyncPreempt is ~100 bytes, check if ELR is within [asyncPreempt, asyncPreempt+256)
	SUB	R10, R11, R12  // R12 = ELR - asyncPreemptAddr
	CMP	$256, R12
	BLO	timer_no_preempt  // If ELR within 256 bytes of asyncPreempt, skip

	// Clear NeedsAsyncPreempt flag
	MOVW	$0, R12
	MOVW	R12, kmazarin∕kirq·NeedsAsyncPreempt(SB)

	// Set up preemption return values
	// R20 = NewELR (asyncPreempt address)
	// R21 = NewSP (unchanged, from exception frame)
	// R22 = NewLR (original ELR, so asyncPreempt can return)
	// R23 = DoPreempt (1 = true)
	MOVD	R10, R20                        // NewELR = asyncPreemptAddr
	MOVD	EXC_FRAME_SP_EL0(RSP), R21      // NewSP = original SP
	MOVD	R11, R22                        // NewLR = original ELR
	MOVD	$1, R23                         // DoPreempt = true

	B	irq_write_eoir

timer_no_preempt:
	// No preemption - cooperative preemption via stackguard0 is still active
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23

	B	irq_write_eoir

irq_not_timer:
	// ========================================================================
	// Non-timer IRQs - Dispatch through kirq.DispatchNonTimerIRQ
	// ========================================================================
	// Store IRQ number in global for Go function to read (avoids ABI complexity)
	// R0 still contains the masked IRQ number from earlier
	MOVD	R0, kmazarin∕kirq·CurrentIRQNum(SB)

	// Call Go dispatcher - handles UART, etc.
	// NOTE: This is safe because:
	//   1. We're on the exception stack (SP_EL1)
	//   2. R28 still has g pointer
	//   3. DispatchNonTimerIRQ is //go:nosplit
	CALL	kmazarin∕kirq·DispatchNonTimerIRQ(SB)

	// Non-timer IRQs don't trigger preemption (only timer does)
	MOVD	$0, R20
	MOVD	$0, R21
	MOVD	$0, R22
	MOVD	$0, R23

irq_write_eoir:
	// Write End Of Interrupt (must do before modifying frame!)
	MOVD	$(GIC_CPU_BASE + GICC_EOIR), R10
	MOVW	R19, (R10)  // Write original IAR value to EOIR

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
	// Restore SP_EL0
	MOVD	EXC_FRAME_SP_EL0(RSP), R10
	MSR	R10, SP_EL0

	// Restore ELR and SPSR
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
// asyncPreemptWrapper - Wrapper that calls asyncPreempt2 directly
// ============================================================================
// We can't use asyncPreempt directly because it saves LR to R28, but
// asyncPreempt2 expects R28 to be g. Instead, we call asyncPreempt2 directly
// while keeping g (R28) valid.
//
// When called (via ERET from IRQ handler):
//   LR = original interrupted PC (set by IRQ handler)
//   R28 = g (restored from exception frame)
//
// The wrapper:
//   1. Saves LR (original PC) and R19 to stack (R19 is callee-saved)
//   2. Copies LR to R19 for safekeeping
//   3. Calls asyncPreempt2 directly (R28/g remains valid)
//   4. Restores LR from R19 and restores R19
//   5. Returns to original PC
//
TEXT ·asyncPreemptWrapper(SB), NOSPLIT, $16
	// Allocate 16 bytes of stack (Go assembler handles prologue)
	// Save R19 (callee-saved) so we can use it to hold LR
	MOVD	R19, 8(RSP)

	// Save original LR to R19 (asyncPreempt2 must preserve R19)
	MOVD	LR, R19

	// Call asyncPreempt2 directly
	// R28 (g) is valid, R19 holds our return address
	BL	runtime·asyncPreempt2(SB)

	// Restore LR from R19
	MOVD	R19, LR

	// Restore R19
	MOVD	8(RSP), R19

	// Return to original PC (now in LR)
	RET

// ============================================================================
// GetExceptionVectorBase - Returns address of exception vector table
// ============================================================================
TEXT ·GetExceptionVectorBase(SB), NOSPLIT, $0-8
	MOVD	$·ExceptionVectorTable(SB), R0
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
