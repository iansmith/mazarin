//go:build !test_stubs

// exceptions_amd64.s - x86_64 exception/interrupt handlers
//
// Exception frame layout (from SP after full save):
//   SP+0:   RAX
//   SP+8:   RBX
//   SP+16:  RCX
//   SP+24:  RDX
//   SP+32:  RSI
//   SP+40:  RDI
//   SP+48:  RBP
//   SP+56:  R8
//   SP+64:  R9
//   SP+72:  R10
//   SP+80:  R11
//   SP+88:  R12
//   SP+96:  R13
//   SP+104: R14 (g register)
//   SP+112: R15
//   SP+120: Error code (pushed by CPU or handler)
//   SP+128: RIP (pushed by CPU)
//   SP+136: CS (pushed by CPU)
//   SP+144: RFLAGS (pushed by CPU)
//   SP+152: RSP (pushed by CPU)
//   SP+160: SS (pushed by CPU)

#include "textflag.h"

// ============================================================================
// ISR Stubs - each pushes a dummy error code (if needed) and vector number
// ============================================================================

// Macro-like ISR entries for exceptions without error codes
// We manually push $0 as the error code

// Vector 14: Page Fault (#PF) - CPU pushes error code
TEXT ·isr14(SB), NOSPLIT|NOFRAME, $0
	// Error code already pushed by CPU
	JMP	common_exception_entry(SB)

// Vector 48 (0x30): LAPIC Timer IRQ
TEXT ·isr48(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	JMP	common_exception_entry(SB)

// Vector 128 (0x80): Syscall via INT $0x80
TEXT ·isr128(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	JMP	common_exception_entry(SB)

// Vector 255 (0xFF): Spurious interrupt
TEXT ·isr255(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	JMP	common_exception_entry(SB)

// ============================================================================
// Common Exception Entry - saves all GPRs and dispatches
// ============================================================================
// On entry: stack has [error_code, RIP, CS, RFLAGS, RSP, SS] from CPU/stub
// We need to save the vector number somewhere. Since we JMP here from different
// ISR stubs, we use a global to pass the vector number.
//
TEXT common_exception_entry(SB), NOSPLIT|NOFRAME, $0
	// Save all general purpose registers
	PUSHQ	R15
	PUSHQ	R14		// g register
	PUSHQ	R13
	PUSHQ	R12
	PUSHQ	R11
	PUSHQ	R10
	PUSHQ	R9
	PUSHQ	R8
	PUSHQ	BP
	PUSHQ	DI
	PUSHQ	SI
	PUSHQ	DX
	PUSHQ	CX
	PUSHQ	BX
	PUSHQ	AX

	// SP now points to the exception frame
	// Frame pointer = SP
	MOVQ	SP, DI		// DI = frame pointer (first arg for Go calls)

	// Read the vector number from the global set by the ISR stub
	MOVQ	·currentVector(SB), SI	// SI = vector number

	// Determine exception type and dispatch
	CMPQ	SI, $128		// INT 0x80 = syscall?
	JE	handle_syscall
	CMPQ	SI, $14			// #PF = page fault?
	JE	handle_page_fault
	CMPQ	SI, $48			// 0x30 = timer IRQ?
	JE	handle_timer_irq
	// Default: dispatch as generic IRQ
	JMP	handle_generic_irq

handle_syscall:
	// Syscall dispatch
	// Frame: [RAX..R15, errcode, RIP, CS, RFLAGS, RSP, SS]
	// syscall number is in RAX (from the INT $0x80 convention)
	// args in RDI, RSI, RDX, R10, R8, R9

	// Save ELR (RIP) and SPSR (RFLAGS) for clone
	MOVQ	128(SP), AX		// RIP from frame
	SUBQ	$16, SP
	MOVQ	AX, 8(SP)
	CALL	·SetSyscallELR(SB)
	ADDQ	$16, SP

	MOVQ	144(SP), AX		// RFLAGS from frame
	SUBQ	$16, SP
	MOVQ	AX, 8(SP)
	CALL	·SetSyscallSPSR(SB)
	ADDQ	$16, SP

	// Dispatch syscall: SyscallDispatch(num, a0, a1, a2, a3, a4, a5)
	// Read syscall number from saved RAX in frame
	MOVQ	0(SP), AX		// syscall num from saved RAX
	SUBQ	$72, SP			// 7 args + 1 return = 64 bytes, aligned
	MOVQ	AX, 8(SP)		// syscallNum
	MOVQ	40+72(SP), AX		// RDI from frame (arg0) [40 + frame adjustment]
	MOVQ	AX, 16(SP)
	MOVQ	32+72(SP), AX		// RSI (arg1)
	MOVQ	AX, 24(SP)
	MOVQ	24+72(SP), AX		// RDX (arg2)
	MOVQ	AX, 32(SP)
	MOVQ	72+72(SP), AX		// R10 (arg3)
	MOVQ	AX, 40(SP)
	MOVQ	56+72(SP), AX		// R8 (arg4)
	MOVQ	AX, 48(SP)
	MOVQ	64+72(SP), AX		// R9 (arg5)
	MOVQ	AX, 56(SP)
	CALL	·SyscallDispatch(SB)
	MOVQ	64(SP), AX		// return value
	ADDQ	$72, SP

	// Store return value in saved RAX slot
	MOVQ	AX, 0(SP)

	// Check if context switch needed
	SUBQ	$16, SP
	CALL	·GetSyscallSwitchTarget(SB)
	MOVQ	8(SP), AX		// target pointer or -1
	ADDQ	$16, SP

	CMPQ	AX, $0
	JLE	exception_return	// No switch needed (0 or -1)

	// Context switch needed
	MOVQ	SP, DI			// frame pointer
	SUBQ	$32, SP
	MOVQ	DI, 8(SP)		// framePtr
	MOVQ	AX, 16(SP)		// targetPtr
	CALL	·DoContextSwitch(SB)
	MOVQ	24(SP), R12		// new context pointer
	ADDQ	$32, SP

	TESTQ	R12, R12
	JZ	exception_return

	// Load new context and IRETQ
	JMP	load_context_and_iretq

handle_page_fault:
	// Read CR2 for fault address
	MOVQ	CR2, AX

	// Call HandlePageFaultAsm(faultAddr) -> handled
	SUBQ	$24, SP
	MOVQ	AX, 8(SP)
	CALL	·HandlePageFaultAsm(SB)
	MOVQ	16(SP), AX		// handled?
	ADDQ	$24, SP

	// If not handled, try userspace handler
	TESTQ	AX, AX
	JNZ	exception_return

	MOVQ	CR2, AX
	SUBQ	$24, SP
	MOVQ	AX, 8(SP)
	CALL	·HandleUserPageFaultAsm(SB)
	ADDQ	$24, SP

	JMP	exception_return

handle_timer_irq:
	// Send EOI to LAPIC first
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0

	// Call timer IRQ handler (assembly preempt handler)
	// For now, dispatch via Go
	MOVQ	$48, AX			// IRQ number
	MOVQ	SP, BX			// frame pointer
	SUBQ	$72, SP
	MOVQ	AX, 8(SP)		// irqNum
	MOVQ	BX, 16(SP)		// framePtr
	MOVQ	128+72(SP), AX		// RIP (ELR equivalent)
	MOVQ	AX, 24(SP)
	MOVQ	152+72(SP), AX		// RSP (SP_EL0 equivalent)
	MOVQ	AX, 32(SP)
	CALL	·TimerIRQHandler(SB)
	ADDQ	$72, SP

	// Check thread preemption
	SUBQ	$24, SP
	MOVQ	SP, AX
	ADDQ	$24, AX			// frame pointer
	MOVQ	AX, 8(SP)
	CALL	·CheckThreadPreemption(SB)
	MOVQ	16(SP), R12		// new context or 0
	ADDQ	$24, SP

	TESTQ	R12, R12
	JZ	exception_return
	JMP	load_context_and_iretq

handle_generic_irq:
	// Send EOI and return
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0
	JMP	exception_return

// ============================================================================
// Exception return - restore GPRs and IRETQ
// ============================================================================
exception_return:
	POPQ	AX
	POPQ	BX
	POPQ	CX
	POPQ	DX
	POPQ	SI
	POPQ	DI
	POPQ	BP
	POPQ	R8
	POPQ	R9
	POPQ	R10
	POPQ	R11
	POPQ	R12
	POPQ	R13
	POPQ	R14
	POPQ	R15
	ADDQ	$8, SP		// Skip error code
	IRETQ

// ============================================================================
// Load new thread context and IRETQ
// ============================================================================
// R12 = pointer to ThreadContext
load_context_and_iretq:
	// Discard current exception frame
	// Build new IRETQ frame from context
	// First, switch to a known good stack (exception stack)

	// Flush TLB
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Use the context's RSP to build the IRETQ frame
	// We need a scratch stack. Use exception stack from PerCPU.
	// For now, just build on current stack after adjusting.

	// Clear the frame and build fresh IRETQ frame
	MOVQ	136(R12), AX		// new RSP
	MOVQ	128(R12), BX		// new RFLAGS
	MOVQ	120(R12), CX		// new RIP

	// Find a safe SP location (below current frame)
	LEAQ	-48(SP), SP		// Make room

	// Build IRETQ frame
	MOVQ	$0, 32(SP)		// SS
	MOVQ	AX, 24(SP)		// RSP
	MOVQ	BX, 16(SP)		// RFLAGS
	MOVQ	$0x08, 8(SP)		// CS
	MOVQ	CX, 0(SP)		// RIP

	// Load GPRs from context
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
	MOVQ	104(R12), R14		// g
	MOVQ	112(R12), R15
	MOVQ	88(R12), R12		// Load R12 last

	IRETQ

// ============================================================================
// Global state for vector number passing
// ============================================================================
// currentVector stores the current interrupt/exception vector number.
// Set by ISR stubs before jumping to common handler.
// This is per-CPU safe because interrupts are disabled during handling.
GLOBL	·currentVector(SB), NOPTR, $8
