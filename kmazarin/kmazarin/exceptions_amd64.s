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

// Vector 1: Debug Exception (#DB) - no error code
// Minimal handler: clear DR6 and resume.
TEXT ·isr1(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	AX
	XORQ	AX, AX
	MOVQ	AX, DR6
	POPQ	AX
	IRETQ

// func getISR1Addr() uintptr
TEXT ·getISR1Addr(SB), NOSPLIT, $0-8
	LEAQ	·isr1(SB), AX
	MOVQ	AX, ret+0(FP)
	RET

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
	// DEBUG: GP fault breadcrumb
	PUSHQ	AX; PUSHQ	DX
	MOVW	$0x3F8, DX; MOVB	$'G', AX; OUTB
	POPQ	DX; POPQ	AX
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
// Device ISR Stubs - vectors 32-47 (IOAPIC device interrupts)
// ============================================================================
// Each stub pushes a dummy error code, sets currentVector, and jumps to
// common_exception_entry. handle_device_irq dispatches based on the vector.

TEXT ·isrDev32(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$32, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev33(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$33, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev34(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$34, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev35(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$35, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev36(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$36, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev37(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$37, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev38(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$38, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev39(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$39, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev40(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$40, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev41(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$41, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev42(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$42, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev43(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$43, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev44(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$44, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev45(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$45, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev46(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$46, ·currentVector(SB)
	JMP	common_exception_entry(SB)

TEXT ·isrDev47(SB), NOSPLIT|NOFRAME, $0
	PUSHQ	$0
	MOVQ	$47, ·currentVector(SB)
	JMP	common_exception_entry(SB)

// initDeviceISRTable fills deviceISRAddrs[0..15] with the addresses of
// isrDev32..isrDev47 via LEAQ. Called from BuildIDT before registering entries.
TEXT ·initDeviceISRTable(SB), NOSPLIT, $0
	LEAQ	·isrDev32(SB), AX
	MOVQ	AX, ·deviceISRAddrs+0(SB)
	LEAQ	·isrDev33(SB), AX
	MOVQ	AX, ·deviceISRAddrs+8(SB)
	LEAQ	·isrDev34(SB), AX
	MOVQ	AX, ·deviceISRAddrs+16(SB)
	LEAQ	·isrDev35(SB), AX
	MOVQ	AX, ·deviceISRAddrs+24(SB)
	LEAQ	·isrDev36(SB), AX
	MOVQ	AX, ·deviceISRAddrs+32(SB)
	LEAQ	·isrDev37(SB), AX
	MOVQ	AX, ·deviceISRAddrs+40(SB)
	LEAQ	·isrDev38(SB), AX
	MOVQ	AX, ·deviceISRAddrs+48(SB)
	LEAQ	·isrDev39(SB), AX
	MOVQ	AX, ·deviceISRAddrs+56(SB)
	LEAQ	·isrDev40(SB), AX
	MOVQ	AX, ·deviceISRAddrs+64(SB)
	LEAQ	·isrDev41(SB), AX
	MOVQ	AX, ·deviceISRAddrs+72(SB)
	LEAQ	·isrDev42(SB), AX
	MOVQ	AX, ·deviceISRAddrs+80(SB)
	LEAQ	·isrDev43(SB), AX
	MOVQ	AX, ·deviceISRAddrs+88(SB)
	LEAQ	·isrDev44(SB), AX
	MOVQ	AX, ·deviceISRAddrs+96(SB)
	LEAQ	·isrDev45(SB), AX
	MOVQ	AX, ·deviceISRAddrs+104(SB)
	LEAQ	·isrDev46(SB), AX
	MOVQ	AX, ·deviceISRAddrs+112(SB)
	LEAQ	·isrDev47(SB), AX
	MOVQ	AX, ·deviceISRAddrs+120(SB)
	RET

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
	// Save XMM registers FIRST — Go code in ALL handlers (PF, timer, syscall)
	// uses XMM registers. For page faults, the CPU retries the faulting instruction
	// which may depend on XMM state loaded before the fault. For timer/device IRQs,
	// the interrupted code may use XMM. Single-CPU so global buffer is safe.
	MOVOU	X0, ·xmmSaveArea+0(SB)
	MOVOU	X1, ·xmmSaveArea+16(SB)
	MOVOU	X2, ·xmmSaveArea+32(SB)
	MOVOU	X3, ·xmmSaveArea+48(SB)
	MOVOU	X4, ·xmmSaveArea+64(SB)
	MOVOU	X5, ·xmmSaveArea+80(SB)
	MOVOU	X6, ·xmmSaveArea+96(SB)
	MOVOU	X7, ·xmmSaveArea+112(SB)
	MOVOU	X8, ·xmmSaveArea+128(SB)
	MOVOU	X9, ·xmmSaveArea+144(SB)
	MOVOU	X10, ·xmmSaveArea+160(SB)
	MOVOU	X11, ·xmmSaveArea+176(SB)
	MOVOU	X12, ·xmmSaveArea+192(SB)
	MOVOU	X13, ·xmmSaveArea+208(SB)
	MOVOU	X14, ·xmmSaveArea+224(SB)
	MOVOU	X15, ·xmmSaveArea+240(SB)

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

	// Save FS_BASE — but ONLY when exception came from userspace (CS=0x1B).
	// Nested kernel exceptions (CS=0x08) must NOT overwrite savedExcFSBase,
	// because it already holds the user FS_BASE from the outermost exception.
	// Without this guard, a page fault during syscall processing overwrites
	// savedExcFSBase with the kernel FS_BASE, and the outer exception_return
	// restores the kernel value to userspace — causing load_g to read from
	// kmazarin memory (the REPEAT fault at 0x439D1500).
	CMPQ	136(SP), $0x08		// CS from exception frame
	JE	exc_entry_skip_fsbase	// Kernel mode: leave savedExcFSBase alone
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, ·savedExcFSBase(SB)
exc_entry_skip_fsbase:

	// Read the vector number from the global set by the ISR stub
	MOVQ	·currentVector(SB), SI	// SI = vector number

	// Determine exception type and dispatch
	CMPQ	SI, $128		// INT 0x80 = syscall?
	JE	handle_syscall
	CMPQ	SI, $129		// SYSCALL instruction (x86_64 userspace)
	JE	handle_syscall
	CMPQ	SI, $14			// #PF = page fault?
	JE	handle_page_fault
	CMPQ	SI, $48			// 0x30 = timer IRQ?
	JE	handle_timer_irq
	CMPQ	SI, $32			// vectors 0-31 = CPU exceptions
	JB	handle_generic_irq
	CMPQ	SI, $48			// vectors 32-47 = IOAPIC device IRQs
	JB	handle_device_irq
	// Default: dispatch as generic IRQ (vectors 49+, except those matched above)
	JMP	handle_generic_irq

handle_syscall:
	// Mark that we're inside syscall processing (unsafe to preempt).
	// Timer handler checks svcDepth to avoid preempting mid-syscall.
	MOVL	$1, ·svcDepth(SB)

	// Syscall dispatch
	// Frame: [RAX..R15, errcode, RIP, CS, RFLAGS, RSP, SS]
	// syscall number is in RAX (from the INT $0x80 convention)
	// args in RDI, RSI, RDX, R10, R8, R9

	// For SYSCALL from userspace (vector 129), switch to kernel g0.
	// The user's R14 is already saved in the frame by common_exception_entry.
	// We need kmazarin's g0 for all Go function calls (ABIInternal uses R14,
	// ABI0 wrappers read g from TLS at FS_BASE-8).
	// This matches ARM64's pattern: every exception handler switches X28 to
	// kmazarinG0Addr before calling any Go code.
	CMPQ	·currentVector(SB), $129
	JNE	syscall_skip_g_setup

	// Switch FS_BASE to kernel value (saved by common_exception_entry).
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR

	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)		// Write kernel g to kernel TLS slot
	MOVQ	DX, R14			// Set R14 to kernel g for ABIInternal calls

syscall_skip_g_setup:
	// Save ELR (RIP) and SPSR (RFLAGS) for clone
	MOVQ	128(SP), R13		// RIP from frame (callee-saved scratch)
	GO_CALL_1_0(·SetSyscallELR, R13)

	MOVQ	144(SP), R13		// RFLAGS from frame
	GO_CALL_1_0(·SetSyscallSPSR, R13)

	// Save callee-saved registers for clone on AMD64.
	// Standard Go runtime's clone keeps mp(R13), gp(R9), fn(R12) in registers
	// instead of storing on child stack (like ARM64 does).
	MOVQ	88(SP), R13		// R12 (fn) from frame
	MOVQ	96(SP), BX		// R13 (mp) from frame
	MOVQ	64(SP), CX		// R9 (gp) from frame
	GO_CALL_3_0(·SetSyscallCloneRegs, R13, BX, CX)

	// Dispatch syscall: SyscallDispatch(num, a0, a1, a2, a3, a4, a5) int64
	// Load all 7 args from exception frame into registers before macro
	MOVQ	0(SP), R8		// syscall num (saved RAX)
	MOVQ	40(SP), R9		// arg0 (saved RDI)
	MOVQ	32(SP), R10		// arg1 (saved RSI)
	MOVQ	24(SP), R11		// arg2 (saved RDX)
	MOVQ	72(SP), BX		// arg3 (saved R10)
	MOVQ	56(SP), CX		// arg4 (saved R8)
	MOVQ	64(SP), DI		// arg5 (saved R9)

	GO_CALL_7_1(·SyscallDispatch, R8, R9, R10, R11, BX, CX, DI)
	// AX = return value
	MOVQ	AX, 0(SP)		// Store return value in saved RAX slot

	// Check if rt_sigreturn was called (SigreturnPending flag).
	// If set, the thread's Context has been restored from the signal frame
	// and we need to load it via load_context_and_iretq instead of normal return.
	MOVQ	·CurrentThread(SB), R13
	TESTQ	R13, R13
	JZ	no_sigreturn
	MOVQ	·ThreadSigreturnPendingOffset(SB), R15
	ADDQ	R13, R15
	MOVL	(R15), AX		// Load SigreturnPending (uint32)
	TESTL	AX, AX
	JZ	no_sigreturn
	// Clear SigreturnPending flag
	MOVL	$0, (R15)
	// Load ThreadContext pointer into R12 for load_context_and_iretq
	MOVQ	·ThreadContextOffset(SB), R12
	ADDQ	R13, R12
	MOVL	$0, ·svcDepth(SB)		// Clear before context switch
	JMP	load_context_and_iretq
no_sigreturn:
	// If a syscall changed FS_BASE (e.g., arch_prctl ARCH_SET_FS),
	// update savedExcFSBase so exception_return preserves the new value.
	// If FS_BASE is still the kernel value (unchanged), keep the original
	// saved user FS_BASE for restoration.
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = current FS_BASE
	CMPQ	AX, ·kmazarinFSBase(SB)
	JE	syscall_fsbase_unchanged
	// FS_BASE was changed by syscall — capture new value
	MOVQ	AX, ·savedExcFSBase(SB)
syscall_fsbase_unchanged:
	// Check if context switch needed
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	// AX = switch target (0 or -1 = no switch)
	CMPQ	AX, $0
	JLE	syscall_no_switch

	MOVQ	AX, R12			// save target in callee-saved R12

	// Context switch: DoContextSwitch(framePtr, targetPtr) uintptr
	MOVQ	SP, DI			// frame pointer
	GO_CALL_2_1(·DoContextSwitch, DI, R12)
	MOVQ	AX, R12			// new context pointer

	TESTQ	R12, R12
	JZ	syscall_exit_no_ctx

	// Load new context and IRETQ
	MOVL	$0, ·svcDepth(SB)		// Clear before context switch
	JMP	load_context_and_iretq

syscall_exit_no_ctx:
	MOVL	$0, ·svcDepth(SB)		// Clear before exception return
	JMP	exception_return

syscall_no_switch:
	MOVL	$0, ·svcDepth(SB)		// Clear before exception return
	JMP	exception_return

handle_page_fault:
	// XMM registers already saved at common_exception_entry.
	// FS_BASE already saved by common_exception_entry.

	// Switch FS_BASE to kernel value so TLS write targets mapped kernel memory.
	// Without this, user FS_BASE may point to unmapped TLS → nested #PF → triple fault.
	// Guard: during early Go runtime init (before kmazarin's init() runs),
	// kmazarinFSBase is 0. In that case, the current FS_BASE/R14 are already
	// valid kernel values (set by diplomat), so skip the FS_BASE/TLS switch.
	MOVQ	·kmazarinFSBase(SB), AX
	TESTQ	AX, AX
	JZ	pf_skip_fsbase_setup	// kmazarinFSBase not yet initialized

	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR				// FS_BASE = kernel TLS base

	// Write kernel g to kernel TLS slot (safe — always mapped)
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)		// Write kernel g to kernel TLS slot
	MOVQ	DX, R14			// Set R14 to kernel g for ABIInternal calls
pf_skip_fsbase_setup:

	// Read CR2 for fault address
	MOVQ	CR2, R13		// save in callee-saved R13

	GO_CALL_1_1(·HandlePageFaultAsm, R13)

	TESTQ	AX, AX
	JNZ	pf_restore_xmm_return	// handled by kernel

	// DIAGNOSTIC: If fault is in kernel mode (CS=0x08) AND it's an instruction
	// fetch (error code bit 4), this is a kernel bug — kernel jumped to a
	// user-space address. Print "KX=<RIP> CR2=<fault>" and halt.
	// This catches bogus interface dispatch or corrupted function pointers
	// before the demand pager maps garbage code pages.
	MOVQ	136(SP), AX		// CS from exception frame
	CMPQ	AX, $0x08
	JNE	try_user_pf_handler
	MOVQ	120(SP), AX		// error code
	TESTQ	$0x10, AX		// bit 4 = I/D (instruction fetch)
	JZ	try_user_pf_handler	// data access: still allow user handler

	// Kernel mode instruction fetch at user-space address — print and halt
	MOVW	$0x3F8, DX
	MOVB	$'K', AX; OUTB		// 'KX=' prefix = Kernel eXecute fault
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	128(SP), R15		// RIP from exception frame
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB		// 'CR2=' = fault address
	MOVB	$'R', AX; OUTB
	MOVB	$'2', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	CR2, R15
	CALL	pf_print_hex16(SB)
	// Print G= (go register from exception frame)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'G', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	104(SP), R15		// R14 (g) from exception frame
	CALL	pf_print_hex16(SB)
	// Print faulting RSP value
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R15		// faulting RSP
	CALL	pf_print_hex16(SB)
	// Print RBP from exception frame
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	48(SP), R15		// RBP from exception frame
	CALL	pf_print_hex16(SB)
	// Print 6 stack words from faulting RSP
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'K', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R12		// faulting RSP (callee-saved)
	MOVQ	0(R12), R15; CALL pf_print_hex16(SB)
	MOVW	$0x3F8, DX; MOVB $' ', AX; OUTB
	MOVQ	8(R12), R15; CALL pf_print_hex16(SB)
	MOVW	$0x3F8, DX; MOVB $' ', AX; OUTB
	MOVQ	16(R12), R15; CALL pf_print_hex16(SB)
	MOVW	$0x3F8, DX; MOVB $' ', AX; OUTB
	MOVQ	24(R12), R15; CALL pf_print_hex16(SB)
	MOVW	$0x3F8, DX; MOVB $' ', AX; OUTB
	MOVQ	32(R12), R15; CALL pf_print_hex16(SB)
	MOVW	$0x3F8, DX; MOVB $' ', AX; OUTB
	MOVQ	40(R12), R15; CALL pf_print_hex16(SB)
	// Print thread 0 context RIP for comparison
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'0', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	// threadListData[0].Context.RIP = threadListData + ThreadContextOffset + 120
	MOVQ	·ThreadContextOffset(SB), R13
	LEAQ	·threadListData(SB), R12
	ADDQ	R13, R12		// R12 = &threadListData[0].Context
	MOVQ	120(R12), R15		// Context.RIP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	136(R12), R15		// Context.RSP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(R12), R15		// Context.CS
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	JMP	pf_unhandled_halt

pf_restore_xmm_return:
	// XMM restore now handled by exception_return (eret_rip_ok)
	JMP	exception_return

try_user_pf_handler:
	// Not handled by kernel — try userspace handler
	// R13 still has fault address (callee-saved, preserved across GO_CALL)
	// Compute isPermFault from page fault error code: bit 0 (P) set → protection fault
	// (page present but wrong permissions), not a missing-page translation fault.
	MOVQ	120(SP), R12   // error code from exception frame
	ANDQ	$1, R12        // R12 = P bit (1 = protection/permission fault, 0 = not-present fault)
	GO_CALL_2_1(·HandleUserPageFaultAsm, R13, R12)

	TESTQ	AX, AX
	JZ	pf_neither_handled	// not handled

	// Check if page fault handler requested a context switch (file-backed mmap page fill)
	GO_CALL_0_1(·GetSyscallSwitchTarget)
	CMPQ	AX, $0
	JLE	pf_restore_xmm_return	// no switch needed, return normally

	MOVQ	AX, R12			// save target in callee-saved R12

	// Context switch: DoContextSwitch(framePtr, targetPtr) uintptr
	MOVQ	SP, DI			// frame pointer
	GO_CALL_2_1(·DoContextSwitch, DI, R12)
	MOVQ	AX, R12			// new context pointer

	TESTQ	R12, R12
	JZ	pf_restore_xmm_return	// no context, return normally

	JMP	load_context_and_iretq

pf_neither_handled:

	// Neither handler resolved the fault. Print CR2 and RIP for debugging.
	// Format: "F:" + hex_fault_addr + "@" + hex_rip + newline
	MOVW	$0x3F8, DX
	MOVB	$'F', AX
	OUTB
	MOVB	$':', AX
	OUTB
	MOVQ	CR2, R15		// save fault addr in R15 (will be restored from frame)

	// Print 16 hex nibbles of R15 (fault addr)
	MOVQ	$60, CX			// start shift = 60 (nibble 15)
pf_hex_loop:
	MOVQ	R15, AX
	SHRQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	pf_hex_ok
	ADDQ	$('A'-'0'-10), AX
pf_hex_ok:
	MOVW	$0x3F8, DX
	OUTB
	SUBQ	$4, CX
	JGE	pf_hex_loop

	MOVW	$0x3F8, DX
	MOVB	$'@', AX
	OUTB

	// Print RIP from exception frame (offset 128 = 16*8)
	MOVQ	128(SP), R15
	MOVQ	$60, CX
pf_rip_loop:
	MOVQ	R15, AX
	SHRQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	pf_rip_ok
	ADDQ	$('A'-'0'-10), AX
pf_rip_ok:
	MOVW	$0x3F8, DX
	OUTB
	SUBQ	$4, CX
	JGE	pf_rip_loop

	// Print CS from exception frame as "CS=" + 2 hex digits
	MOVW	$0x3F8, DX
	MOVB	$'C', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	136(SP), R11		// CS from frame
	MOVQ	R11, AX
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	pf_cs1
	ADDQ	$('A'-'0'-10), AX
pf_cs1:	OUTB
	MOVQ	R11, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	pf_cs2
	ADDQ	$('A'-'0'-10), AX
pf_cs2:	OUTB

	// Print " BX=" + RBX from exception frame (possible argv pointer)
	MOVB	$' ', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	8(SP), R15		// BX from frame
	CALL	pf_print_hex16(SB)

	// Print " SI=" + RSI from exception frame
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	32(SP), R15		// SI from frame
	CALL	pf_print_hex16(SB)

	// Print " uSP=" + RSP from exception frame
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'u', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R15		// RSP from frame (user SP)
	CALL	pf_print_hex16(SB)

	// Print " FS=" + FS_BASE from savedExcFSBase
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·savedExcFSBase(SB), R15
	CALL	pf_print_hex16(SB)

	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB

	// Check if fault came from user mode (CS in exception frame)
	MOVQ	136(SP), AX		// CS from exception frame
	CMPQ	AX, $0x08		// kernelCS?
	JNE	pf_user_fault		// User fault — try kill+switch

	// Kernel-mode unhandled page fault — print full diagnostic and halt.
	// Print "G=" then R14 (g pointer) from exception frame
	MOVW	$0x3F8, DX
	MOVB	$'G', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	104(SP), R15		// R14 from frame
	CALL	pf_print_hex16(SB)

	// Print " BP=" then saved RBP
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	48(SP), R15		// BP from frame
	CALL	pf_print_hex16(SB)

	// Print " SP=" then faulting RSP
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R15		// RSP from frame
	CALL	pf_print_hex16(SB)

	// Print " EC=" then error code from exception frame
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	120(SP), R15		// error code
	CALL	pf_print_hex16(SB)

	// Print saved GPRs from exception frame that are useful for diagnosis:
	// RAX (0), RCX (16), RDX (24)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'A', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	0(SP), R15		// RAX from frame
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	16(SP), R15		// RCX from frame
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	24(SP), R15		// RDX from frame
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	32(SP), R15		// RSI from frame
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	40(SP), R15		// RDI from frame
	CALL	pf_print_hex16(SB)

	// Print "\nSTK=" then 6 words from the faulting stack
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'K', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R12		// R12 = faulting RSP (callee-saved)
	MOVQ	0(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	8(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	16(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	24(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	32(R12), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	40(R12), R15
	CALL	pf_print_hex16(SB)

	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	JMP	pf_unhandled_halt

pf_user_fault:

	// User page fault not handled — map to signal and deliver or kill shepherd.
	// Switch FS_BASE to kernel value for Go call.
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)
	MOVQ	DX, R14

	// Save exception frame pointer and extract fault info into callee-saved regs.
	MOVQ	SP, BP			// BP = exception frame base
	MOVQ	$14, R13		// excInfo = vector 14 (page fault)
	MOVQ	CR2, R12		// faultAddr = CR2
	MOVQ	128(SP), BX		// faultPC = RIP from exception frame

	// CRITICAL: Save the full exception context to ThreadContext BEFORE calling
	// HandleUnhandledExceptionAsm. Without this, BuildSignalFrame reads stale
	// SP/LR/register values from ThreadContext, causing the Go runtime's
	// stack traceback to read garbage and throw "unknown caller pc".
	GO_CALL_1_0(·SaveContextFromFrame, BP)

	// Call HandleUnhandledExceptionAsm(vector=14, CR2, RIP)
	GO_CALL_3_1(·HandleUnhandledExceptionAsm, R13, R12, BX)
	TESTQ	AX, AX
	JZ	pf_unhandled_halt	// 0 = error, halt
	MOVQ	AX, R12			// R12 = ThreadContext (signal or next thread)
	JMP	load_context_and_iretq

pf_unhandled_halt:
	HLT
	JMP	pf_unhandled_halt

handle_timer_irq:
	// Switch FS_BASE to kernel value (saved by common_exception_entry).
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR

	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)		// Write kernel g to kernel TLS slot
	MOVQ	DX, R14			// Set R14 to kernel g for ABIInternal calls

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

	// Process deadline-based wakeups (matching ARM64/RISC-V timer handlers).
	// Without this, deadline-based goroutine wakeups (nanosleep, timers)
	// never fire, stalling the scheduler and blocking sysmon.
	GO_CALL_0_0(·ProcessDeadlinesTopHalf)

	// Kernel-mode preemption decision.
	// User-mode (CS=0x1B): always allow preemption.
	// Kernel-mode (CS=0x08): allow preemption ONLY if:
	//   1. Post-boot (kmazarinSyscallReady != 0) — early boot has no threads
	//   2. svcDepth == 0 — not inside a syscall handler (preempting mid-syscall
	//      corrupts the exception frame on the shared stack)
	CMPQ	136(SP), $0x08		// CS from exception frame
	JNE	timer_preempt_allowed	// User mode — always allow
	// Kernel mode: check if safe to preempt
	MOVL	runtime·kmazarinSyscallReady(SB), AX
	TESTL	AX, AX
	JZ	irq_exception_return	// Early boot — no preemption
	MOVL	·svcDepth(SB), AX
	TESTL	AX, AX
	JNZ	irq_exception_return	// Inside syscall handler — unsafe to preempt

	// Ring 0, svcDepth==0: check kernel goroutine async preemption.
	// The Go runtime sets m.signalPending when GC/sysmon wants to preempt.
	// CheckKernelGoroutinePreempt calls isAsyncSafePoint and injects
	// asyncPreempt into the exception frame if safe.
	// g (R14) is already set to g0 from handle_timer_irq.
	MOVQ	SP, R13
	GO_CALL_1_1(·CheckKernelGoroutinePreempt, R13)
	TESTQ	AX, AX
	JNZ	irq_exception_return	// frame was modified, skip thread preempt

	JMP	timer_preempt_check
timer_preempt_allowed:
timer_preempt_check:

	// Check NeedsThreadPreempt flag (matching ARM64 pattern at exceptions_arm64.s:985).
	// TimerIRQHandler sets this flag (via timerIRQHandlerInternal) when the
	// current thread's preemption deadline has expired. Without this guard,
	// every timer tick would unconditionally preempt — causing a hot bounce
	// loop where the user thread never completes a syscall.
	MOVL	mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB), AX
	TESTL	AX, AX
	JZ	irq_exception_return

	// NOTE: m.locks check removed for userspace thread preemption.
	// Each shepherd runs in its own address space with isolated Go runtime state.
	// Context-switching freezes and restores the full CPU state atomically —
	// the shepherd resumes exactly where it was interrupted, locks intact.
	// Ring 0 (kernel) preemption was already filtered out above.
	// Clear NeedsThreadPreempt flag
	MOVL	$0, mazzy∕kmazarin∕kirq·NeedsThreadPreempt(SB)

	// Thread preemption needed — save and switch
	MOVQ	SP, R13			// frame pointer (before macro SUB)
	GO_CALL_1_1(·CheckThreadPreemption, R13)
	MOVQ	AX, R12			// new context pointer

	TESTQ	R12, R12
	JZ	irq_exception_return

	// Increment context switch counter (matches ARM64 exceptions_arm64.s:1549)
	LEAQ	·timerCtxSwitchCount(SB), AX
	INCQ	(AX)

	JMP	load_context_and_iretq

handle_device_irq:
	// Device interrupt (vectors 32-47, used by MSI-X on AMD64).
	// Switch FS_BASE to kernel value (saved by common_exception_entry).
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR

	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)		// Write kernel g to kernel TLS slot
	MOVQ	DX, R14			// Set R14 to kernel g for ABIInternal calls

	// Store IOAPIC input number (vector - 32) in topHalfIRQNum.
	// This matches the convention used by ARM64 (GIC SPI) and RISC-V (PLIC source):
	// Go code sees the same IRQ number that devices registered with.
	MOVQ	SI, AX
	SUBQ	$32, AX
	MOVQ	AX, ·topHalfIRQNum(SB)

	// Call NonTimerIRQTopHalf to drain device used rings and wake slots.
	// For level-triggered PCI INTx, this reads the VirtIO ISR register
	// to deassert the interrupt BEFORE we send EOI.
	GO_CALL_0_0(·NonTimerIRQTopHalf)

	// Send EOI to LAPIC AFTER source deassert.
	// For level-triggered interrupts, EOI before deassert causes re-delivery.
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0

	JMP	irq_exception_return

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
	// For fault vectors (0-31), print diagnostic info then halt
	CMPQ	SI, $32
	JB	generic_fault_diag
	// For IRQs (32+), send EOI and return
	MOVQ	$(0xFEE00000 + 0xFFFFFFFF00000000), AX
	MOVL	$0, 0xB0(AX)		// LAPIC_EOI = 0
	JMP	irq_exception_return

generic_fault_diag:
	// Print error code (at 120(SP)) and faulting RIP (at 128(SP))
	MOVW	$0x3F8, DX
	MOVB	$'E', AX
	OUTB
	MOVB	$'=', AX
	OUTB
	// Print error code as 8 hex digits
	MOVQ	120(SP), R11
	MOVQ	R11, AX; SHRQ $28, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe0; ADDQ $('A'-'0'-10), AX
fe0:	OUTB
	MOVQ	R11, AX; SHRQ $24, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe1; ADDQ $('A'-'0'-10), AX
fe1:	OUTB
	MOVQ	R11, AX; SHRQ $20, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe2; ADDQ $('A'-'0'-10), AX
fe2:	OUTB
	MOVQ	R11, AX; SHRQ $16, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe3; ADDQ $('A'-'0'-10), AX
fe3:	OUTB
	MOVQ	R11, AX; SHRQ $12, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe4; ADDQ $('A'-'0'-10), AX
fe4:	OUTB
	MOVQ	R11, AX; SHRQ $8, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe5; ADDQ $('A'-'0'-10), AX
fe5:	OUTB
	MOVQ	R11, AX; SHRQ $4, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe6; ADDQ $('A'-'0'-10), AX
fe6:	OUTB
	MOVQ	R11, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB fe7; ADDQ $('A'-'0'-10), AX
fe7:	OUTB
	// Print faulting RIP
	MOVB	$'@', AX
	MOVW	$0x3F8, DX
	OUTB
	// Print full 8-byte RIP from 128(SP)
	MOVQ	128(SP), R11
	// Nibble 15 (bits 60-63)
	MOVQ	R11, AX
	SHRQ	$60, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip0
	ADDQ	$('A'-'0'-10), AX
frip0:	OUTB
	// Nibble 14 (bits 56-59)
	MOVQ	R11, AX
	SHRQ	$56, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip1
	ADDQ	$('A'-'0'-10), AX
frip1:	OUTB
	// Nibble 13
	MOVQ	R11, AX
	SHRQ	$52, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip2
	ADDQ	$('A'-'0'-10), AX
frip2:	OUTB
	// Nibble 12
	MOVQ	R11, AX
	SHRQ	$48, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip3
	ADDQ	$('A'-'0'-10), AX
frip3:	OUTB
	// Nibble 11
	MOVQ	R11, AX
	SHRQ	$44, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip4
	ADDQ	$('A'-'0'-10), AX
frip4:	OUTB
	// Nibble 10
	MOVQ	R11, AX
	SHRQ	$40, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip5
	ADDQ	$('A'-'0'-10), AX
frip5:	OUTB
	// Nibble 9
	MOVQ	R11, AX
	SHRQ	$36, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip6
	ADDQ	$('A'-'0'-10), AX
frip6:	OUTB
	// Nibble 8
	MOVQ	R11, AX
	SHRQ	$32, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip7
	ADDQ	$('A'-'0'-10), AX
frip7:	OUTB
	// Nibble 7
	MOVQ	R11, AX
	SHRQ	$28, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip8
	ADDQ	$('A'-'0'-10), AX
frip8:	OUTB
	// Nibble 6
	MOVQ	R11, AX
	SHRQ	$24, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	frip9
	ADDQ	$('A'-'0'-10), AX
frip9:	OUTB
	// Nibble 5
	MOVQ	R11, AX
	SHRQ	$20, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripA
	ADDQ	$('A'-'0'-10), AX
fripA:	OUTB
	// Nibble 4
	MOVQ	R11, AX
	SHRQ	$16, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripB
	ADDQ	$('A'-'0'-10), AX
fripB:	OUTB
	// Nibble 3
	MOVQ	R11, AX
	SHRQ	$12, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripC
	ADDQ	$('A'-'0'-10), AX
fripC:	OUTB
	// Nibble 2
	MOVQ	R11, AX
	SHRQ	$8, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripD
	ADDQ	$('A'-'0'-10), AX
fripD:	OUTB
	// Nibble 1
	MOVQ	R11, AX
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripE
	ADDQ	$('A'-'0'-10), AX
fripE:	OUTB
	// Nibble 0
	MOVQ	R11, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	fripF
	ADDQ	$('A'-'0'-10), AX
fripF:	OUTB
	MOVB	$'\n', AX
	MOVW	$0x3F8, DX
	OUTB

	// Print RAX from saved GPR frame (offset 0 = RAX)
	MOVW	$0x3F8, DX
	MOVB	$'R', AX; OUTB
	MOVB	$'A', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	0(SP), R15		// saved RAX
	CALL	pf_print_hex16(SB)
	// Print RSP from exception frame (offset 152)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	152(SP), R15		// faulting RSP
	CALL	pf_print_hex16(SB)
	// Print CS from exception frame (offset 136)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	136(SP), R15		// CS
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB

	// Check if fault came from user mode (CS in exception frame)
	// Frame layout: 15 GPRs (0-112) + error code (120) + RIP (128) + CS (136)
	MOVQ	136(SP), AX		// CS from exception frame
	CMPQ	AX, $0x08		// kernelCS?
	JE	generic_halt		// Kernel fault → halt

	// User fault: map to signal and deliver or kill shepherd.
	// Switch FS_BASE to kernel value.
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX
	WRMSR
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	·kmazarinG0Addr(SB), DX
	MOVQ	DX, -8(AX)
	MOVQ	DX, R14

	// Save exception frame pointer and extract fault info into callee-saved regs.
	MOVQ	SP, BP				// BP = exception frame base
	MOVQ	·currentVector(SB), R13	// excInfo = vector number
	MOVQ	$0, R12			// faultAddr = 0 (no CR2 relevant for non-PF)
	MOVQ	128(SP), BX		// faultPC = RIP from exception frame

	// CRITICAL: Save the full exception context to ThreadContext BEFORE calling
	// HandleUnhandledExceptionAsm. Without this, BuildSignalFrame reads stale
	// register values from ThreadContext.
	GO_CALL_1_0(·SaveContextFromFrame, BP)

	// Call HandleUnhandledExceptionAsm(vector, 0, RIP)
	GO_CALL_3_1(·HandleUnhandledExceptionAsm, R13, R12, BX)
	TESTQ	AX, AX
	JZ	generic_halt		// 0 = error, halt
	MOVQ	AX, R12			// R12 = ThreadContext (signal or next thread)
	JMP	load_context_and_iretq

generic_halt:
	// Print 'G' to indicate entering generic HLT loop
	MOVW	$0x3F8, DX
	MOVB	$'G', AX
	OUTB
generic_halt_loop:
	HLT
	JMP	generic_halt_loop

// ============================================================================
// IRQ exception return — PrepareForExceptionExit (x86_64)
// ============================================================================
// Timer and device IRQ handlers jump here instead of exception_return.
// Hardware IRQs always interrupt running code that had IF=1 (interrupts must
// be enabled for the IRQ to fire). The CPU saves RFLAGS with IF=1 and clears
// IF for the handler. However, under QEMU TCG on Apple Silicon, the saved
// RFLAGS can have IF=0 — possibly a TCG translation bug. This trampoline
// forces IF=1 in the saved RFLAGS before restoring, preventing the interrupted
// kernel code from resuming with IF=0 (which causes HLT hangs in doBlockIO).
//
// Page faults and other exceptions use exception_return directly because they
// can legitimately occur during CLI critical sections where IF=0 must be
// preserved.
irq_exception_return:
	ORQ	$0x200, 144(SP)		// Force IF=1 in saved RFLAGS on stack

// ============================================================================
// Exception return - restore GPRs and IRETQ
// ============================================================================
exception_return:
	// FS_BASE handling depends on whether we're returning to user or kernel mode.
	//
	// User return (CS=0x1B): WRMSR to restore savedExcFSBase (user TLS base).
	//   Do NOT write to [FS_BASE - 8] — user TLS page may be demand-paged.
	//   R14 (g register) is restored from the GPR frame by the POPs below.
	//
	// Kernel return (CS=0x08): Skip WRMSR — FS_BASE is already the kernel value
	//   (set by the handler's entry code). Sync g to kernel TLS using kmazarinFSBase.
	//   This is critical for nested exceptions: savedExcFSBase holds the USER value
	//   from the outermost exception, NOT the kernel value we need for the WRMSR.
	CMPQ	136(SP), $0x08		// CS from exception frame
	JE	eret_kernel_tls		// Kernel mode — skip WRMSR, just sync g

	// User mode return: restore user FS_BASE
	MOVQ	·savedExcFSBase(SB), AX
	MOVQ	AX, DX
	SHRQ	$32, DX			// EDX = high32
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR				// Restore user FS_BASE
	JMP	eret_skip_tls_write	// Don't write g to user TLS (may fault)

eret_kernel_tls:
	// Kernel mode return: FS_BASE is already kernel value, sync g to kernel TLS.
	// Guard: if kmazarinFSBase is 0 (early init), skip TLS write.
	MOVQ	·kmazarinFSBase(SB), AX
	TESTQ	AX, AX
	JZ	eret_skip_tls_write
	MOVQ	104(SP), DX		// R14 from saved GPR frame
	MOVQ	DX, -8(AX)		// Write g to kernel TLS slot (safe)
eret_skip_tls_write:

	// Restore XMM registers saved at common_exception_entry.
	// Without this, timer/device IRQ handlers clobber XMM via Go's memmove,
	// corrupting the interrupted code's MOVDQU operations.
	MOVOU	·xmmSaveArea+0(SB), X0
	MOVOU	·xmmSaveArea+16(SB), X1
	MOVOU	·xmmSaveArea+32(SB), X2
	MOVOU	·xmmSaveArea+48(SB), X3
	MOVOU	·xmmSaveArea+64(SB), X4
	MOVOU	·xmmSaveArea+80(SB), X5
	MOVOU	·xmmSaveArea+96(SB), X6
	MOVOU	·xmmSaveArea+112(SB), X7
	MOVOU	·xmmSaveArea+128(SB), X8
	MOVOU	·xmmSaveArea+144(SB), X9
	MOVOU	·xmmSaveArea+160(SB), X10
	MOVOU	·xmmSaveArea+176(SB), X11
	MOVOU	·xmmSaveArea+192(SB), X12
	MOVOU	·xmmSaveArea+208(SB), X13
	MOVOU	·xmmSaveArea+224(SB), X14
	MOVOU	·xmmSaveArea+240(SB), X15

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

	// Fix IF=0 in RFLAGS for user mode returns. User code must always
	// run with interrupts enabled. Silently force IF=1 if needed.
	// NOTE: Kernel mode IF=0 must be preserved — page faults during CLI
	// critical sections (SaveAndDisableIRQs) must return with IF=0 to
	// maintain the invariant. IRQ returns force IF=1 via irq_exception_return.
	TESTQ	$0x200, 16(SP)		// Check IF in RFLAGS
	JNZ	eret_if_ok
	CMPQ	8(SP), $0x1B		// Check CS = user code?
	JNE	eret_if_ok		// Kernel mode IF=0 is OK (page fault in CLI section)
	ORQ	$0x200, 16(SP)		// Force IF=1 for user return
eret_if_ok:

	// DEBUG: Check RIP in IRETQ frame before returning
	// SP now points to [RIP, CS, RFLAGS, RSP, SS]
	CMPQ	0(SP), $0x100000
	JAE	eret_rip_ok
	// Bad RIP — print "!ER=" + RIP + " V=" + vector + "\n"
	PUSHQ	AX
	PUSHQ	DX
	MOVW	$0x3F8, DX
	MOVB	$'!', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	// Print RIP (16(SP) because we pushed AX and DX)
	MOVQ	16(SP), AX
	// Print 8 hex nibbles (top 32 bits)
	MOVQ	AX, R11
	SHRQ	$28, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er0; ADDQ $('A'-'0'-10), AX
er0:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $24, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er1; ADDQ $('A'-'0'-10), AX
er1:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $20, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er2; ADDQ $('A'-'0'-10), AX
er2:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $16, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er3; ADDQ $('A'-'0'-10), AX
er3:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $12, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er4; ADDQ $('A'-'0'-10), AX
er4:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $8, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er5; ADDQ $('A'-'0'-10), AX
er5:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; SHRQ $4, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er6; ADDQ $('A'-'0'-10), AX
er6:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB er7; ADDQ $('A'-'0'-10), AX
er7:	MOVW $0x3F8, DX; OUTB
	// Print " V=" + vector
	MOVB $' ', AX; OUTB
	MOVB $'V', AX; OUTB
	MOVB $'=', AX; OUTB
	MOVQ ·currentVector(SB), AX
	MOVQ AX, R11
	SHRQ $4, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB ev0; ADDQ $('A'-'0'-10), AX
ev0:	MOVW $0x3F8, DX; OUTB
	MOVQ R11, AX; ANDQ $0xF, AX; ADDQ $'0', AX; CMPB AX, $('9'+1); JB ev1; ADDQ $('A'-'0'-10), AX
ev1:	MOVW $0x3F8, DX; OUTB
	MOVB $'\n', AX; OUTB
	POPQ	DX
	POPQ	AX
	// Continue to IRETQ — will page fault and diagnostic there will provide more info
eret_rip_ok:
	// Restore XMM registers saved at common_exception_entry
	MOVOU	·xmmSaveArea+0(SB), X0
	MOVOU	·xmmSaveArea+16(SB), X1
	MOVOU	·xmmSaveArea+32(SB), X2
	MOVOU	·xmmSaveArea+48(SB), X3
	MOVOU	·xmmSaveArea+64(SB), X4
	MOVOU	·xmmSaveArea+80(SB), X5
	MOVOU	·xmmSaveArea+96(SB), X6
	MOVOU	·xmmSaveArea+112(SB), X7
	MOVOU	·xmmSaveArea+128(SB), X8
	MOVOU	·xmmSaveArea+144(SB), X9
	MOVOU	·xmmSaveArea+160(SB), X10
	MOVOU	·xmmSaveArea+176(SB), X11
	MOVOU	·xmmSaveArea+192(SB), X12
	MOVOU	·xmmSaveArea+208(SB), X13
	MOVOU	·xmmSaveArea+224(SB), X14
	MOVOU	·xmmSaveArea+240(SB), X15
	IRETQ

// ============================================================================
// Load new thread context and IRETQ
// ============================================================================
// R12 = pointer to ThreadContext
load_context_and_iretq:
	// Validate CS from context before building IRETQ frame
	MOVQ	152(R12), R13		// CS from context
	CMPQ	R13, $0x08		// kernelCS
	JE	cs_valid
	CMPQ	R13, $0x1B		// userCS
	JE	cs_valid
	// Invalid CS — print diagnostic "!CS=" + hex value to COM1, then halt
	MOVW	$0x3F8, DX
	MOVB	$'!', AX
	OUTB
	MOVB	$'C', AX
	OUTB
	MOVB	$'S', AX
	OUTB
	MOVB	$'=', AX
	OUTB
	// Print CS value as 2 hex digits
	MOVQ	R13, AX
	SHRQ	$4, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	cs_hex1
	ADDQ	$('A'-'0'-10), AX
cs_hex1:
	OUTB
	MOVQ	R13, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	cs_hex2
	ADDQ	$('A'-'0'-10), AX
cs_hex2:
	OUTB
	MOVB	$'\n', AX
	OUTB
cs_halt:
	// Print 'C' to indicate CS validation failure HLT
	MOVW	$0x3F8, DX
	MOVB	$'C', AX
	OUTB
cs_halt_loop:
	HLT
	JMP	cs_halt_loop
cs_valid:
	// Flush TLB
	MOVQ	CR3, AX
	MOVQ	AX, CR3

	// Restore FS_BASE from context (per-thread TLS base).
	// Without this, userspace threads that called arch_prctl(ARCH_SET_FS)
	// would resume with the wrong FS_BASE, causing TLS reads to return
	// garbage and crashing the Go runtime.
	//
	// CRITICAL: If FSBase==0 (e.g., kernel Thread 0 before any TLS setup),
	// skip BOTH the WRMSR and the TLS sync write. Otherwise the TLS sync
	// reads the stale FS_BASE from the previous thread and writes g to
	// a user-space address, corrupting user heap data (allgs corruption).
	MOVQ	144(R12), AX		// FSBase from ThreadContext
	TESTQ	AX, AX
	JZ	skip_fsbase_and_tls	// FSBase==0 → skip WRMSR AND TLS sync
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR				// Restore FS_BASE

	// Sync TLS: write the new thread's g register to the TLS slot.
	// TLS layout on Linux amd64: g is at FS_BASE - 8 (i.e. FS:-8).
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = FS_BASE
	MOVQ	104(R12), DX		// DX = new g (context.R14)
	MOVQ	DX, -8(AX)		// Write g to TLS slot
skip_fsbase_and_tls:

	// Use the context's RSP to build the IRETQ frame
	// We need a scratch stack. Use exception stack from PerCPU.
	// For now, just build on current stack after adjusting.

	// Clear the frame and build fresh IRETQ frame
	MOVQ	136(R12), AX		// new RSP
	MOVQ	128(R12), BX		// new RFLAGS
	// Force IF=1 in RFLAGS for threads restored after boot.
	// During early boot (kmazarinSyscallReady=0), threads inherit IF=0 to
	// prevent APIC timer interrupts before IDT/LAPIC are ready. After boot,
	// ALL threads (kernel AND user) must run with IF=1 so timer preemption
	// works. Without this, kernel clone children (sysmon, templateThread)
	// created during init with IF=0 run forever without timer interrupts,
	// starving thread 0 and halting the system.
	MOVQ	152(R12), R13		// CS from context
	CMPQ	R13, $0x1B		// userCS?
	JE	rflags_force_if		// User thread: always force IF=1
	// Kernel thread: force IF=1 only after boot
	MOVL	runtime·kmazarinSyscallReady(SB), R13
	TESTL	R13, R13
	JZ	rflags_no_fix		// Early boot: trust inherited IF state
rflags_force_if:
	ORQ	$0x200, BX		// Force IF=1
rflags_no_fix:
	MOVQ	120(R12), CX		// new RIP

	// DEBUG: Validate RIP before IRETQ — catch null/low pointers
	CMPQ	CX, $0x100000
	JAE	rip_ok
	// RIP < 0x100000 — print "!RIP=" + hex_rip + " CTX=" + hex_R12 + "\n"
	PUSHQ	CX
	PUSHQ	R12
	MOVW	$0x3F8, DX
	MOVB	$'!', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	// Print CX (RIP) as 16 hex nibbles (must use CX for shift)
	MOVQ	CX, R15
	MOVQ	$60, R13
rip_diag_loop:
	MOVQ	R15, AX
	MOVQ	R13, CX
	SHRQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	rip_diag_ok
	ADDQ	$('A'-'0'-10), AX
rip_diag_ok:
	MOVW	$0x3F8, DX; OUTB
	SUBQ	$4, R13
	JGE	rip_diag_loop
	// Print " CTX="
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	// Print R12 (context pointer) as 16 hex nibbles
	MOVQ	R12, R15
	MOVQ	$60, R13
ctx_diag_loop:
	MOVQ	R15, AX
	MOVQ	R13, CX
	SHRQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	ctx_diag_ok
	ADDQ	$('A'-'0'-10), AX
ctx_diag_ok:
	MOVW	$0x3F8, DX; OUTB
	SUBQ	$4, R13
	JGE	ctx_diag_loop
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	POPQ	R12
	POPQ	CX
	// Continue with IRETQ anyway (will page fault, but handler will print more info)
rip_ok:

	// Find a safe SP location (below current frame)
	LEAQ	-48(SP), SP		// Make room

	// Build IRETQ frame with CS/SS from ThreadContext
	MOVQ	160(R12), R13		// SS from context (offset 20*8)
	MOVQ	R13, 32(SP)		// SS in IRETQ frame
	MOVQ	AX, 24(SP)		// RSP
	MOVQ	BX, 16(SP)		// RFLAGS
	MOVQ	152(R12), R13		// CS from context (offset 19*8)
	MOVQ	R13, 8(SP)		// CS in IRETQ frame
	MOVQ	CX, 0(SP)		// RIP

	// Restore XMM registers saved at common_exception_entry.
	// NOTE: This restores the interrupted thread's XMM, not the target thread's.
	// Proper per-thread XMM state requires extending ThreadContext (future work).
	// For now, this prevents kernel Go code's XMM garbage from leaking to user code.
	MOVOU	·xmmSaveArea+0(SB), X0
	MOVOU	·xmmSaveArea+16(SB), X1
	MOVOU	·xmmSaveArea+32(SB), X2
	MOVOU	·xmmSaveArea+48(SB), X3
	MOVOU	·xmmSaveArea+64(SB), X4
	MOVOU	·xmmSaveArea+80(SB), X5
	MOVOU	·xmmSaveArea+96(SB), X6
	MOVOU	·xmmSaveArea+112(SB), X7
	MOVOU	·xmmSaveArea+128(SB), X8
	MOVOU	·xmmSaveArea+144(SB), X9
	MOVOU	·xmmSaveArea+160(SB), X10
	MOVOU	·xmmSaveArea+176(SB), X11
	MOVOU	·xmmSaveArea+192(SB), X12
	MOVOU	·xmmSaveArea+208(SB), X13
	MOVOU	·xmmSaveArea+224(SB), X14
	MOVOU	·xmmSaveArea+240(SB), X15

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

// svcDepth (declared in percpu.go as var svcDepth uint32) tracks whether
// the CPU is inside a syscall handler. Set to 1 at handle_syscall entry,
// cleared before exception_return / load_context_and_iretq. The timer handler
// checks svcDepth to decide if kernel-mode preemption is safe.

// pf_print_hex16 prints R15 as 16 hex chars to COM1. Clobbers AX, CX, DX, R13.
TEXT pf_print_hex16(SB), NOSPLIT|NOFRAME, $0
	MOVQ	$60, CX
pf_ph_loop:
	MOVQ	R15, AX
	MOVQ	CX, R13
	SHRQ	CX, AX
	ANDQ	$0xF, AX
	ADDQ	$'0', AX
	CMPB	AX, $('9'+1)
	JB	pf_ph_ok
	ADDQ	$('A'-'0'-10), AX
pf_ph_ok:
	MOVW	$0x3F8, DX
	OUTB
	MOVQ	R13, CX
	SUBQ	$4, CX
	JGE	pf_ph_loop
	RET

// ============================================================================
// SYSCALL Entry - entered via x86_64 SYSCALL instruction
// ============================================================================
// SYSCALL sets: RCX=return RIP, R11=RFLAGS, clears IF via FMASK.
// We build a fake exception frame compatible with common_exception_entry,
// then route through the same handler as INT $0x80 with vector=129 for
// syscall number translation (x86_64→ARM64).
//
// Normally reached from Ring 3 userspace (overlayed INT $0x80 for Ring 0).
// However, Go compiler-generated code (e.g. time.now's VDSO fallback,
// syscall.rawSyscallNoError) can issue SYSCALL from Ring 0. We detect this
// by checking the saved RSP: kernel stacks use high canonical addresses
// (bit 63 = 1), user stacks use low canonical addresses (bit 63 = 0).
//
// Not using SYSRET for return — IRETQ handles all cases safely.
TEXT ·syscallEntry(SB), NOSPLIT|NOFRAME, $0
	// Save RCX (return RIP) and R11 (RFLAGS) to scratch space.
	// SYSCALL clobbers these: RCX=return RIP, R11=RFLAGS.
	MOVQ	CX, ·syscallScratchRCX(SB)
	MOVQ	R11, ·syscallScratchR11(SB)

	// Switch to kernel exception stack
	MOVQ	SP, ·syscallScratchRSP(SB)
	MOVQ	·excStackTopForSyscall(SB), SP

	// Detect Ring 0 vs Ring 3 SYSCALL by checking the caller's RSP.
	// Kernel stacks use high canonical addresses (bit 63 = 1).
	// User stacks use low canonical addresses (bit 63 = 0).
	// This handles compiler-generated SYSCALL instructions (e.g. time.now's
	// VDSO fallback, syscall.rawSyscallNoError) that fire from Ring 0.
	// Without this, exception_return would IRETQ to Ring 3 at a kernel address.
	//
	// Use scratch globals for CS/SS to avoid duplicate PUSH sequences
	// (the Go linker's nosplit checker sums all PUSHQs in a function).
	MOVQ	·syscallScratchRSP(SB), CX
	TESTQ	CX, CX
	JS	syscall_ring0_cs
	MOVQ	$0x23, ·syscallScratchSS(SB)	// Ring 3 data: GDT 0x20 | RPL=3
	MOVQ	$0x1B, ·syscallScratchCS(SB)	// Ring 3 code: GDT 0x18 | RPL=3
	JMP	syscall_build_frame
syscall_ring0_cs:
	MOVQ	$0x10, ·syscallScratchSS(SB)	// Ring 0 data: GDT 0x10
	MOVQ	$0x08, ·syscallScratchCS(SB)	// Ring 0 code: GDT 0x08

syscall_build_frame:
	// Build fake exception frame (single push sequence for nosplit safety)
	PUSHQ	·syscallScratchSS(SB)		// SS
	PUSHQ	·syscallScratchRSP(SB)		// RSP (caller's original)
	MOVQ	·syscallScratchR11(SB), CX
	PUSHQ	CX				// RFLAGS (saved by CPU in R11)
	PUSHQ	·syscallScratchCS(SB)		// CS
	MOVQ	·syscallScratchRCX(SB), CX
	PUSHQ	CX				// RIP (saved by CPU in RCX)
	PUSHQ	$0				// Error code (none)

	// Restore R11 to original RFLAGS value for GPR save in common_exception_entry
	MOVQ	·syscallScratchR11(SB), R11
	MOVQ	$129, ·currentVector(SB)	// 129 = SYSCALL (vs 128 = INT $0x80)
	JMP	common_exception_entry(SB)

// Scratch space for SYSCALL entry (per-CPU safe: interrupts disabled by FMASK)
GLOBL	·syscallScratchRCX(SB), NOPTR, $8
GLOBL	·syscallScratchR11(SB), NOPTR, $8
GLOBL	·syscallScratchRSP(SB), NOPTR, $8
GLOBL	·syscallScratchCS(SB), NOPTR, $8
GLOBL	·syscallScratchSS(SB), NOPTR, $8

// XMM save area for page fault handler. 256 bytes = 16 XMM registers × 16 bytes.
// Single-CPU so a global buffer is safe (no concurrent access).
GLOBL	·xmmSaveArea(SB), NOPTR, $256

// sigreturnTrampoline — invokes rt_sigreturn via INT $0x80.
// This is the return address pushed on the signal stack by BuildSignalFrame.
// When sigtramp's handler returns (RET), execution lands here.
TEXT ·sigreturnTrampoline(SB), NOSPLIT|NOFRAME, $0
	MOVL	$15, AX			// SYS_rt_sigreturn on x86_64
	INT	$0x80
	INT	$3			// Should not return

// getSigreturnTrampolineAddr — returns the address of sigreturnTrampoline.
TEXT ·getSigreturnTrampolineAddr(SB), NOSPLIT, $0-8
	LEAQ	·sigreturnTrampoline(SB), AX
	MOVQ	AX, ret+0(FP)
	RET



