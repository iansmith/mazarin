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
//
// x86_64 CALL convention: CALL pushes 8-byte return address onto stack.
// Therefore, when calling a Go function, place args at 0(SP), 8(SP), 16(SP)...
// CALL shifts them to 8(callee_SP), 16(callee_SP)... where the callee expects them.

#include "textflag.h"
#include "go_abi_macros_amd64.h"

// ============================================================================
// ISR Stubs - each pushes a dummy error code (if needed) and vector number
// ============================================================================

// Macro-like ISR entries for exceptions without error codes
// We manually push $0 as the error code

// Vector 0: Divide Error (#DE) - no error code
TEXT ·isr0(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	MOVQ	$0, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 6: Invalid Opcode (#UD) - no error code
TEXT ·isr6(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	MOVQ	$6, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 8: Double Fault (#DF) - CPU pushes error code (always 0)
TEXT ·isr8(SB), NOSPLIT|NOFRAME, $0
	// Error code already pushed by CPU
	MOVQ	$8, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 13: General Protection (#GP) - CPU pushes error code
TEXT ·isr13(SB), NOSPLIT|NOFRAME, $0
	// Error code already pushed by CPU
	MOVQ	$13, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 14: Page Fault (#PF) - CPU pushes error code
TEXT ·isr14(SB), NOSPLIT|NOFRAME, $0
	// Error code already pushed by CPU
	MOVQ	$14, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 48 (0x30): LAPIC Timer IRQ
TEXT ·isr48(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	MOVQ	$48, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 128 (0x80): Syscall via INT $0x80
TEXT ·isr128(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0		// Dummy error code
	MOVQ	$128, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// Vector 255 (0xFF): Spurious interrupt
TEXT ·isr255(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$255, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// ============================================================================
// ISR Address Getters - return raw code addresses for IDT population
// ============================================================================

// func getISR0Addr() uintptr
TEXT ·getISR0Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr0(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR6Addr() uintptr
TEXT ·getISR6Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr6(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR8Addr() uintptr
TEXT ·getISR8Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr8(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR13Addr() uintptr
TEXT ·getISR13Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr13(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR14Addr() uintptr
TEXT ·getISR14Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr14(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR48Addr() uintptr
TEXT ·getISR48Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr48(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR128Addr() uintptr
TEXT ·getISR128Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr128(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// func getISR255Addr() uintptr
TEXT ·getISR255Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr255(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

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

	// DEBUG: breadcrumb 's' for syscall entry
	MOVW	$0x3F8, DX
	MOVB	$'s', AX
	OUTB

	// Save ELR (RIP) and SPSR (RFLAGS) for clone
	// DEBUG: breadcrumb 'a' before SetSyscallELR
	MOVW	$0x3F8, DX
	MOVB	$'a', AX
	OUTB
	MOVQ	128(SP), R13		// RIP from frame (callee-saved scratch)
	GO_CALL_1_0(·SetSyscallELR, R13)

	// DEBUG: breadcrumb '1' after SetSyscallELR
	MOVW	$0x3F8, DX
	MOVB	$'1', AX
	OUTB

	MOVQ	144(SP), R13		// RFLAGS from frame
	GO_CALL_1_0(·SetSyscallSPSR, R13)

	// DEBUG: breadcrumb '2' after SetSyscallSPSR
	MOVW	$0x3F8, DX
	MOVB	$'2', AX
	OUTB

	// Dispatch syscall: SyscallDispatch(num, a0, a1, a2, a3, a4, a5) int64
	// Load all 7 args from exception frame into registers before macro
	MOVQ	0(SP), R8		// syscall num (saved RAX)
	MOVQ	40(SP), R9		// arg0 (saved RDI)
	MOVQ	32(SP), R10		// arg1 (saved RSI)
	MOVQ	24(SP), R11		// arg2 (saved RDX)
	MOVQ	72(SP), BX		// arg3 (saved R10)
	MOVQ	56(SP), CX		// arg4 (saved R8)
	MOVQ	64(SP), DI		// arg5 (saved R9)

	// DEBUG: breadcrumb '3' before SyscallDispatch
	MOVW	$0x3F8, DX
	MOVB	$'3', AX
	OUTB

	GO_CALL_7_1(·SyscallDispatch, R8, R9, R10, R11, BX, CX, DI)
	// AX = return value
	MOVQ	AX, 0(SP)		// Store return value in saved RAX slot

	// DEBUG: breadcrumb 'r' for syscall dispatch returned
	MOVW	$0x3F8, DX
	MOVB	$'r', AX
	OUTB

	// Check if context switch needed
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	// AX = switch target (0 or -1 = no switch)
	CMPQ	AX, $0
	JLE	exception_return

	// DEBUG: breadcrumb 'C' for context switch
	MOVQ	AX, R12			// save target in callee-saved R12
	MOVW	$0x3F8, DX
	MOVB	$'C', AX
	OUTB

	// Context switch: DoContextSwitch(framePtr, targetPtr) uintptr
	MOVQ	SP, DI			// frame pointer
	GO_CALL_2_1(·DoContextSwitch, DI, R12)
	MOVQ	AX, R12			// new context pointer

	// DEBUG: breadcrumb 'X' for DoContextSwitch returned
	PUSHQ	AX
	MOVW	$0x3F8, DX
	MOVB	$'X', AX
	OUTB
	POPQ	AX

	TESTQ	R12, R12
	JZ	exception_return

	// DEBUG: print new context values before loading
	MOVW	$0x3F8, DX
	MOVB	$'L', AX
	OUTB
	// Print RIP (first 4 hex digits)
	MOVQ	120(R12), R11		// new RIP
	MOVB	$':', AX
	OUTB
	MOVQ	R11, AX
	SHRQ	$28, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip1
	ADDQ	$('A'-'0'-10), AX
lrip1:
	OUTB
	MOVQ	R11, AX
	SHRQ	$24, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip2
	ADDQ	$('A'-'0'-10), AX
lrip2:
	OUTB
	MOVQ	R11, AX
	SHRQ	$20, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip3
	ADDQ	$('A'-'0'-10), AX
lrip3:
	OUTB
	MOVQ	R11, AX
	SHRQ	$16, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip4
	ADDQ	$('A'-'0'-10), AX
lrip4:
	OUTB
	MOVQ	R11, AX
	SHRQ	$12, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip5
	ADDQ	$('A'-'0'-10), AX
lrip5:
	OUTB
	MOVQ	R11, AX
	SHRQ	$8, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip6
	ADDQ	$('A'-'0'-10), AX
lrip6:
	OUTB
	MOVQ	R11, AX
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip7
	ADDQ	$('A'-'0'-10), AX
lrip7:
	OUTB
	MOVQ	R11, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	lrip8
	ADDQ	$('A'-'0'-10), AX
lrip8:
	OUTB
	MOVB	$'\n', AX
	OUTB

	// Load new context and IRETQ
	JMP	load_context_and_iretq

handle_page_fault:
	// DEBUG: breadcrumb 'p' for page fault
	MOVW	$0x3F8, DX
	MOVB	$'p', AX
	OUTB
	// Read CR2 for fault address
	MOVQ	CR2, R13		// save in callee-saved R13

	GO_CALL_1_1(·HandlePageFaultAsm, R13)
	// AX = handled (1) or not (0)
	TESTQ	AX, AX
	JNZ	exception_return

	// Not handled by kernel — try userspace handler
	// R13 still has fault address (callee-saved, preserved across GO_CALL)
	GO_CALL_1_1(·HandleUserPageFaultAsm, R13)

	JMP	exception_return

handle_timer_irq:
	// Send EOI to LAPIC first
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0

	// Call timer IRQ handler
	// TimerIRQHandler(irqNum, framePtr, elr, spEl0 uint64) — 4 args
	// Load all args from exception frame before macro adjusts SP
	MOVQ	$48, R8			// irqNum
	MOVQ	SP, R9			// framePtr (exception frame base)
	MOVQ	128(SP), R10		// elr (RIP from frame)
	MOVQ	152(SP), R11		// spEl0 (RSP from frame)
	GO_CALL_4_0(·TimerIRQHandler, R8, R9, R10, R11)

	// Check thread preemption
	MOVQ	SP, R13			// frame pointer (before macro SUB)
	GO_CALL_1_1(·CheckThreadPreemption, R13)
	MOVQ	AX, R12			// new context pointer

	TESTQ	R12, R12
	JZ	exception_return
	JMP	load_context_and_iretq

handle_generic_irq:
	// Print '!' + 2-digit hex vector number for diagnostic
	MOVW	$0x3F8, DX
	MOVB	$'!', AX
	OUTB
	MOVQ	SI, AX			// vector number (in SI from earlier)
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	gvec1
	ADDQ	$('A'-'0'-10), AX
gvec1:
	OUTB
	MOVQ	SI, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	gvec2
	ADDQ	$('A'-'0'-10), AX
gvec2:
	OUTB
	// For fault vectors (0-31), halt — can't safely return
	CMPQ	SI, $32
	JB	generic_halt
	// For IRQs (32+), send EOI and return
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0
	JMP	exception_return
generic_halt:
	HLT
	JMP	generic_halt

// ============================================================================
// Exception return - restore GPRs and IRETQ
// ============================================================================
exception_return:
	// DEBUG: breadcrumb 'i' for IRETQ return
	MOVW	$0x3F8, DX
	MOVB	$'i', AX
	OUTB
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

	// Sync TLS: write the new thread's g register to the TLS slot.
	// Without this, systemstack() and other ABI0 runtime assembly reads
	// the stale g from TLS (e.g. left by a clone child), causing the parent
	// to use the wrong m/curg/stack and crash.
	// TLS layout on Linux amd64: g is at FS_BASE - 8 (i.e. FS:-8).
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = FS_BASE
	MOVQ	104(R12), DX		// DX = new g (context.R14)
	MOVQ	DX, -8(AX)		// Write g to TLS slot

	// Use the context's RSP to build the IRETQ frame
	// We need a scratch stack. Use exception stack from PerCPU.
	// For now, just build on current stack after adjusting.

	// Clear the frame and build fresh IRETQ frame
	MOVQ	136(R12), AX		// new RSP
	MOVQ	128(R12), BX		// new RFLAGS
	// Clear IF (bit 9) — keep interrupts disabled until LAPIC/IDT are fully configured
	MOVQ	$0x200, CX
	NOTQ	CX
	ANDQ	CX, BX
	MOVQ	120(R12), CX		// new RIP

	// Find a safe SP location (below current frame)
	LEAQ	-48(SP), SP		// Make room

	// Build IRETQ frame
	MOVQ	$0x30, 32(SP)		// SS (UEFI data segment)
	MOVQ	AX, 24(SP)		// RSP
	MOVQ	BX, 16(SP)		// RFLAGS
	MOVQ	$0x38, 8(SP)		// CS (UEFI 64-bit code segment)
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
// ISR Ignore - default handler for unhandled interrupts
// ============================================================================
// isrIgnore is a minimal handler that just returns from the interrupt.
// Used for all interrupt vectors that don't have specific handlers.
// Simply ignores the interrupt and returns to the interrupted code.
TEXT ·isrIgnore(SB), NOSPLIT|NOFRAME, $0
	// Don't push error code - some vectors don't have one
	// Just return immediately via IRETQ
	IRETQ

// getISRIgnoreAddr returns the address of the ignore handler
TEXT ·getISRIgnoreAddr(SB), NOSPLIT, $0-8
	LEAQ	·isrIgnore(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

// ReadCS returns the current CS (Code Segment) selector value.
// This is needed to populate IDT entries with the correct segment selector.
TEXT ·ReadCS(SB), NOSPLIT, $0-2
	MOVW	CS, AX		// Read CS into AX (16-bit value)
	MOVW	AX, ret+0(FP)	// Return as uint16
	RET

// ============================================================================
// Global state for vector number passing
// ============================================================================
// currentVector stores the current interrupt/exception vector number.
// Set by ISR stubs before jumping to common handler.
// This is per-CPU safe because interrupts are disabled during handling.
GLOBL	·currentVector(SB), NOPTR, $8
