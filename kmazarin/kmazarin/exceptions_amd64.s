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
#include "frame_dsl_amd64.h"

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

	// ════════════════════ IST ROTATION (MAZ-136, rev B) ═════════════════════
	// ‼ MINEFIELD — read this WHOLE banner before touching ANY line of it.
	// ‼ Getting this wrong does not crash here; it resurfaces hours later as
	// ‼ silent stack corruption far from the cause.
	//
	// WHY: #PF, timer, and device vectors are IST1-gated (idt_amd64.go). Raw
	// IST semantics reload RSP = TSS.IST1 — a FIXED top — on EVERY delivery.
	// A nested IST-vectored exception (a kernel #PF inside the #PF handler;
	// an IRQ after IF is re-enabled mid-handler) would restart at that same
	// top and TRAMPLE the suspended outer chain. That trample was the
	// MAZ-136 corruption: a RET through an overwritten return-address slot
	// into dead stack (the deterministic 0xFFFFFFFF44125BF0), and
	// HandleUserPageFault's spilled pageAddr zeroed mid-flight (run-5 catch).
	//
	// SCHEME: TSS.IST1 is a single GLOBAL nesting cursor — the x86 equivalent
	// of ARM64's SP_EL1, which nests continuously and never resets per
	// delivery or per thread. Every entry through here SUBTRACTS one stride;
	// every normal return ADDS it back (exception_return); a context switch
	// made from inside a handler ADDS once (load_context_and_iretq — retiring
	// exactly the abandoning handler's own level). The CPU reads the IST
	// fields from TSS MEMORY at each delivery (LTR caches only the base), so
	// the next nested delivery lands one stride below the lowest live
	// reservation — underneath every live frame of every chain.
	//
	// INVARIANT: TSS.IST1 = (IST1-half top) − stride × (number of LIVE
	// exception chains — running or suspended, across ALL threads).
	// Equivalently: the cursor sits below the live frames of every live
	// chain. Induction: each level starts at the then-current IST1 (IST
	// delivery) or at the live SP (nested-in-place ring-0 SYSCALL /
	// INT $0x80), immediately reserves its stride, and physically uses less
	// than one stride (168 B frame + bounded handler chain ≪ 2 KiB) — so its
	// own growth never reaches the lowered cursor, and deeper levels inherit
	// the same argument.
	//
	// ‼ PER-THREAD IST STATE IS FORBIDDEN. Revision A carried a per-thread
	// ctx.ISTBase restored absolutely at context switch. WRONG: the IST half
	// is ONE shared physical region — when thread A was suspended mid-#PF-
	// handler (live chain at the top of the half) and thread B's fresh
	// context reset the cursor to the top, B's next #PF delivered ON TOP of
	// A's live chain (KVM-run-4 trample, identical !F: signature). The
	// cursor must outlive any one thread's tenure on the CPU.
	//
	// BALANCE: every exit from a common_exception_entry frame is exactly one
	// of:
	//   exception_return IRETQ ........ ADD: this level RETURNS .. (balanced)
	//   load_context_and_iretq ........ ADD: this level DIES — its
	//                                   exception_return never runs; any
	//                                   SHALLOWER suspended chains keep
	//                                   their reservations ....... (balanced)
	//   diagnostic halt (KX / !F: / BADIRETQ / BADCTX / ISTOVF /
	//                    RSPOVF / RSPOVR) ...................... (moot)
	// YieldToReadyThread / RunFirstThread deliberately touch NOTHING: they
	// are not exceptions, no chain dies there, and the global cursor already
	// accounts for the suspended chains of every context they switch
	// between. If you add a NEW exit path you MUST give it one of these
	// treatments, or the arithmetic drifts one stride per traversal until
	// ISTOVF halts.
	//
	// KNOWN LEAK (accepted, documented): a thread KILLED while it still owns
	// a suspended live chain never runs that chain's ADDs — those strides
	// leak until ISTOVF flags the drift. If ISTOVF fires with shallow
	// nesting, hunt for exactly this.
	//
	// GATE: ist1Floor is 0 until copyGDTToOwnedBuffer publishes the split;
	// the SUB, the return ADD, and the switch ADD all skip while it is 0.
	// The floor cannot change between one exception's entry and its exit
	// (single CPU; the only writer is straight-line init code that this
	// exception interrupted — see ist1Floor's doc), so the gates can never
	// disagree within one exception.
	//
	// REGISTERS: all GPRs are already saved in the frame above; R12/R13 are
	// scratch and are reloaded by exception_return's POPs. Flags are dead
	// (the interrupted RFLAGS lives in the CPU frame).
	MOVQ	·ist1Floor(SB), R13
	TESTQ	R13, R13
	JZ	ist_rotate_done		// split not published yet (early boot): no rotation
	MOVQ	·tssBuffer+36(SB), R12	// live IST1 (TSS bytes 36-43; unaligned qword is fine on WB memory)
	SUBQ	$const_istRotateStride, R12
	CMPQ	R12, R13		// would rotate below ist1Floor → nesting deeper
	JB	ist_rotate_ovf_tramp	//   than the 8 reserved levels → halt loudly
	MOVQ	R12, ·tssBuffer+36(SB)	// commit: the next delivery lands one stride lower
	INCQ	·istSubCount(SB)	// accounting (see ISTCT in syscall_amd64.go)
	JMP	ist_rotate_done
ist_rotate_ovf_tramp:
	JMP	ist_overflow_dump(SB)	// terminal: prints state, then cli;hlt
ist_rotate_done:
	// ═══════════════════════ end IST ROTATION entry ═════════════════════════

	// ═══════════ RSP0 ROTATION — THE SECOND CURSOR (MAZ-136) ════════════════
	// ‼ MINEFIELD — this is the IST scheme's twin for the OTHER fixed top.
	// ‼ Read the IST ROTATION banner above end to end FIRST; everything there
	// ‼ (invariant, induction, exit-path table, GATE proof, the per-thread
	// ‼ prohibition, the known kill-leak) applies verbatim to this cursor.
	//
	// WHY A SECOND CURSOR: TSS.RSP0 (tssBuffer bytes 4-11) is hardware-
	// loaded as RSP on EVERY ring3→ring0 transition that is NOT IST-vectored
	// (INT $0x80 and user-mode #GP/#DE/#UD — any IDT entry with IST index 0;
	// #PF/timer/device are IST1-vectored and land on the other half). Ring-3
	// SYSCALLs reach the SAME half through syscallEntry's SOFTWARE switch.
	// SyscallWaitSoftIRQ PARKS a live, resumable syscall chain on this half
	// (STI+HLT, interrupts enabled): with a fixed top, the next ring-3 entry
	// landed AT the top and trampled the parked chain, which later RETs
	// through an overwritten return-address slot into dead stack — run 45's
	// execute-the-stack at RIP = RSP0top−0x410, the original deterministic
	// 0xFFFFFFFF44125BF0 geometry. Variant 1 (d15893b2) gated only the
	// SOFTWARE reset in syscallEntry; the HARDWARE reset has no software
	// gate, so the only fix is making TSS.RSP0 itself the moving cursor.
	//
	// UNIFORM ARITHMETIC: both cursors rotate on EVERY entry through here,
	// regardless of which half this frame physically landed on (or neither —
	// a ring-0 #GP nests in place on g0). No per-half classification branch,
	// no second proof: the IST banner's exit-path table balances BOTH
	// cursors, because every listed exit performs both ADDs. The price is
	// pure accounting slack — a chain reserves a stride on the half it does
	// NOT occupy — paid for by the halves' depth budgets (8 IST levels,
	// 14 RSP0 levels of $const_rsp0RotateStride = 8 KiB in the 112 KiB half).
	//
	// THE PARKED-CHAIN RESERVATION IS THE FIX: a suspended chain's entry SUB
	// stays un-retired until that chain itself exits. While SyscallWaitSoftIRQ
	// is parked, its reservation stands — every later ring3→ring0 delivery
	// (hardware RSP0 load or syscallEntry's cursor read) lands one stride
	// BELOW the parked frames instead of on top of them. This is exactly the
	// IST banner's "shallower suspended chains keep their reservations" case;
	// the suspended-resumable mid-syscall kernel threads of the documented
	// svcDepth holes are the same hazard class and are covered the same way.
	//
	// ‼ ONE-LEVEL-USE BOUND (the assumption that keeps 8 KiB safe): one
	// nesting level on the RSP0 half must use LESS than one stride — the
	// 168 B CPU+GPR frame plus the full ring-3 syscall Go dispatch, which
	// GO_CALL runs ON THIS STACK (syscall table, klog, delegate machinery;
	// the parked chain was observed at ~0x410 B). A level that outgrows its
	// stride reaches below the lowered cursor and the next delivery lands
	// inside it anyway — the RSPOVF floor tripwire and the IRETQ guard turn
	// that into a diagnosable halt instead of silent corruption.
	//
	// ‼ THE COUPLING TRAP (the one place this cursor differs from IST1):
	// rotating ONLY the TSS field does NOT fix ring-3 SYSCALLs — SYSCALL
	// switches no stacks in hardware; those entries take syscallEntry's
	// software switch. That switch MUST read this live cursor
	// (·tssBuffer+4), never a fixed top — see the matching banner at
	// syscall_do_switch before touching either site. excStackTopForSyscall
	// is BOOT-WINDOW-ONLY (threads.go).
	//
	// ‼ PER-THREAD RSP0 STATE IS FORBIDDEN — the IST banner's rev-A lesson,
	// same shared-region argument: the RSP0 half is ONE region holding every
	// thread's parked syscall chains; any per-thread reset reuses it over
	// another thread's live chain.
	//
	// INVARIANT: TSS.RSP0 = rsp0Ceil − rsp0RotateStride × (live exception
	// chains, ALL threads). GATE: rsp0Floor (published LAST by
	// copyGDTToOwnedBuffer, after the IST pair; the IST banner's GATE
	// paragraph covers why entry and exit can never disagree). REGISTERS:
	// R12/R13 scratch again — the IST block above is done with them; flags
	// dead (interrupted RFLAGS lives in the CPU frame).
	MOVQ	·rsp0Floor(SB), R13
	TESTQ	R13, R13
	JZ	rsp0_rotate_done	// split not published yet (early boot): no rotation
	MOVQ	·tssBuffer+4(SB), R12	// live RSP0 (TSS bytes 4-11; unaligned qword is fine on WB memory)
	SUBQ	$const_rsp0RotateStride, R12
	CMPQ	R12, R13		// would rotate below rsp0Floor → nesting deeper
	JB	rsp0_rotate_ovf_tramp	//   than the 14 reserved levels → halt loudly
	MOVQ	R12, ·tssBuffer+4(SB)	// commit: the next ring3→ring0 delivery lands lower
	INCQ	·rsp0SubCount(SB)	// accounting (see RSPCT in syscall_amd64.go)
	JMP	rsp0_rotate_done
rsp0_rotate_ovf_tramp:
	JMP	rsp0_overflow_dump(SB)	// terminal: prints state, then cli;hlt
rsp0_rotate_done:
	// ══════════════════════ end RSP0 ROTATION entry ═════════════════════════

	// SP now points to the exception frame
	// Frame pointer = SP
	MOVQ	SP, DI		// DI = frame pointer (first arg for Go calls)

	// Stash the interrupted RIP for readELR_EL1 — the x86 software equivalent of
	// ARM64's ELR_EL1. Frame offset 128 holds the interrupted PC (CS is at 136,
	// checked below). AX is free here: its interrupted value is already saved at
	// 0(SP), and the next user of AX (RDMSR below) reloads it.
	MOVQ	128(SP), AX	// interrupted RIP from the exception frame
	MOVQ	AX, ·interruptedRIP(SB)

	// MAZ-135/MAZ-136: capture the interrupted thread's live kernel TLS-g BEFORE
	// any handler overwrites [kmazarinFSBase-8] with the handler g0 (syscall
	// :403, timer :930, device :1023). For a kernel-mode exception this is the
	// interrupted kernel goroutine's curg — even in the systemstack exit window
	// where frame R14 is the stale g0. The kernel-mode branch of
	// SaveContextFromFrame reads this instead of frame R14, so a preempted
	// kernel thread resumes with a faithful dual-home g. (User-mode exceptions
	// also write this slot with an irrelevant value — unused; the user branch
	// reads [savedExcFSBase-8]. Ring-0 read of mapped kernel memory. Global,
	// single-CPU — same caveat as interruptedRIP/xmmSaveArea.)
	MOVQ	·kmazarinFSBase(SB), AX
	MOVQ	-8(AX), AX
	MOVQ	AX, ·savedExcKernelTLSG(SB)

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
	// R12 = &threadListData[0].Context; read fields by symbolic offset
	MOVQ	·ThreadContextOffset(SB), R13
	LEAQ	·threadListData(SB), R12
	ADDQ	R13, R12		// R12 = &threadListData[0].Context
	FLD(R12, ThreadContext_RIP, R15)	// Context.RIP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_RSP, R15)	// Context.RSP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_CS, R15)		// Context.CS
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

	// IST-rotation accounting at the moment of death (see ISTCT comment in
	// syscall_amd64.go): subs, eret ADDs, load_context ADDs, live cursor.
	// subs−adds must equal the number of LIVE chains; a deficit names the
	// over-retiring site that an exactly-at-ceiling cursor cannot.
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·istSubCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·istEretAddCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'L', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·istLcAddCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+36(SB), R15
	CALL	pf_print_hex16(SB)

	// RSP0-rotation accounting — the second cursor's twin line (see the
	// RSPCT comment in syscall_amd64.go): subs, eret ADDs, load_context
	// ADDs, live RSP0 cursor. Same deficit arithmetic as ISTCT above.
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·rsp0SubCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·rsp0EretAddCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'L', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·rsp0LcAddCount(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+4(SB), R15
	CALL	pf_print_hex16(SB)

	// ---- MAZ-136 KVM-race diagnostics (eager-netpoll × shepherd-launch) ----
	// Three extra lines naming the dying chain, added for the 5/5 KVM-only
	// `!F:0 @mallocgcSmallNoscan` family (continuation-netpoll-kvm-race.md).
	//
	// VEC/SVD: currentVector is global save-state — by dump time it reads
	// this #PF's own vector (0E); printed anyway to expose unexpected entry
	// paths. svcDepth is NOT touched by exception entry, so it still shows
	// whether the dying chain sat inside syscall dispatch.
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15	// 32-bit load zero-extends into R15
	CALL	pf_print_hex16(SB)

	// GMP: discriminate the two possible nil sources behind the allocator
	// fault (`test %al,(%rsi)` with RSI=0 in mallocgcSmallNoscan): m.p == 0
	// with runtime.mcache0 already cleared by procresize, vs m.p != 0 with
	// a nil p.mcache. Field offsets g.m=48, m.p=208, p.mcache=56 are
	// disasm-verified against THIS pinned toolchain's (Go 1.26.2)
	// mallocgcSmallNoscan — diagnostics-only, do not build logic on them.
	// Walked only when the frame's g is the kernel g0, so every load stays
	// inside known-mapped kernel data. Values are read at dump time, i.e.
	// microseconds after the fault — indicative, not a snapshot.
	MOVQ	104(SP), R12		// g (R14) from exception frame
	CMPQ	R12, ·kmazarinG0Addr(SB)
	JNE	pf_gmp_done
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'G', AX; OUTB
	MOVB	$'M', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'M', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	48(R12), R12		// m = g.m
	MOVQ	R12, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	208(R12), R12		// p = m.p
	MOVQ	R12, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'M', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	XORQ	R15, R15		// p == 0 → MC prints 0 (field is moot)
	TESTQ	R12, R12
	JZ	pf_gmp_mc
	MOVQ	56(R12), R15		// mcache = p.mcache
pf_gmp_mc:
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'M', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'0', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	runtime·mcache0(SB), R15
	CALL	pf_print_hex16(SB)
pf_gmp_done:

	// BPW: frame-pointer chain walk, up to 8 return addresses. The walker
	// dereferences BP only while it lies inside the exception stack
	// [excStackBottom, excStackTop) — always mapped, so the walk itself
	// cannot fault; the first frame outside (g0/goroutine stack) or a
	// broken chain ends it early.
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'W', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	48(SP), R12		// walker = RBP from exception frame
	MOVQ	$8, R11			// frame budget
pf_bpw_loop:
	MOVQ	·excStackBottom(SB), AX
	TESTQ	AX, AX
	JZ	pf_bpw_done		// bounds not yet published — cannot walk safely
	CMPQ	R12, AX
	JB	pf_bpw_done		// walker left the exception stack (below)
	MOVQ	·excStackTop(SB), AX
	SUBQ	$16, AX
	CMPQ	R12, AX
	JA	pf_bpw_done		// walker left the exception stack (above)
	MOVQ	8(R12), R15		// this frame's return address
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVQ	0(R12), R12		// next frame pointer
	DECQ	R11
	JNZ	pf_bpw_loop
pf_bpw_done:

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

	// ====================================================================
	// MAZ-135: priority-wake fast path. Immediately schedule an io_uring-woken
	// thread instead of waiting for the next timer quantum. Mirrors
	// exceptions_arm64.s (priority-wake block) and reuses handle_timer_irq's
	// kernel-mode guards. The switch is done by ·CheckThreadPreemption (the same
	// scheduler entry the timer path uses); correctness relies on the faithful
	// dual-home g restore in load_context_and_iretq (ThreadContext.TLSG).
	// ====================================================================
	MOVL	·priorityWakePending(SB), AX
	TESTL	AX, AX
	JZ	irq_exception_return
	MOVL	$0, ·priorityWakePending(SB)		// consume
	LEAQ	·dbgPWakeChecked(SB), AX
	INCL	(AX)

	// Kernel-mode guard, identical to the timer path: user mode (CS=0x1B) is
	// always allowed; kernel mode (CS=0x08) only post-boot, outside a syscall,
	// and only if CheckKernelGoroutinePreempt does not inject asyncPreempt.
	CMPQ	136(SP), $0x08				// interrupted CS
	JNE	device_pwake_allowed			// user mode — allow
	MOVL	runtime·kmazarinSyscallReady(SB), AX
	TESTL	AX, AX
	JZ	irq_exception_return			// early boot — no threads
	MOVL	·svcDepth(SB), AX
	TESTL	AX, AX
	JZ	device_pwake_ksafe
	LEAQ	·dbgPWakeSVC(SB), AX			// inside a syscall — unsafe
	INCL	(AX)
	JMP	irq_exception_return
device_pwake_ksafe:
	MOVQ	SP, R13
	GO_CALL_1_1(·CheckKernelGoroutinePreempt, R13)
	TESTQ	AX, AX
	JNZ	irq_exception_return			// asyncPreempt injected — done

device_pwake_allowed:
	MOVQ	SP, R13
	GO_CALL_1_1(·CheckThreadPreemption, R13)
	MOVQ	AX, R12					// new ThreadContext (or 0)
	TESTQ	R12, R12
	JZ	device_pwake_noctx
	LEAQ	·dbgPWakeSwitched(SB), AX
	INCL	(AX)
	JMP	load_context_and_iretq
device_pwake_noctx:
	LEAQ	·dbgPWakeNoCtx(SB), AX
	INCL	(AX)
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
	// IST ROTATION (MAZ-136) — the balancing ADD for the SUB at the top of
	// common_exception_entry. ‼ Read the banner there BEFORE changing this.
	// Same gate as the entry side: skip while ist1Floor==0 (the floor cannot
	// change between this exception's entry and now — see the banner's GATE
	// paragraph). AX is scratch: the interrupted AX is restored by the POPs
	// below. ADDQ imm,mem keeps it a single instruction (no torn window).
	MOVQ	·ist1Floor(SB), AX
	TESTQ	AX, AX
	JZ	eret_ist_done
	MOVQ	·tssBuffer+36(SB), AX
	ADDQ	$const_istRotateStride, AX	// retire this level's reservation
	CMPQ	AX, ·ist1Ceil(SB)
	JA	eret_ist_overshoot	// cursor above the half top = OVER-RETIRE: some
					//   exit released a still-live reservation — halt
					//   BEFORE the resulting trample, with context
	MOVQ	AX, ·tssBuffer+36(SB)
	INCQ	·istEretAddCount(SB)	// accounting (see ISTCT in syscall_amd64.go)
	JMP	eret_ist_done
eret_ist_overshoot:
	MOVQ	$1, R14			// site code 1 = exception_return (halting; R14 free)
	JMP	ist_overshoot_dump(SB)
eret_ist_done:

	// RSP0 ROTATION (MAZ-136) — the second cursor's balancing ADD, twin of
	// the IST ADD above with the identical gate/tripwire shape. ‼ Read the
	// RSP0 ROTATION banner in common_exception_entry BEFORE changing this.
	// An over-retire here would park the cursor above a still-live parked
	// syscall chain — the next ring-3 delivery lands on it (the run-45
	// trample). AX scratch, single-instruction memory ADD: same as above.
	MOVQ	·rsp0Floor(SB), AX
	TESTQ	AX, AX
	JZ	eret_rsp0_done
	MOVQ	·tssBuffer+4(SB), AX
	ADDQ	$const_rsp0RotateStride, AX	// retire this level's reservation
	CMPQ	AX, ·rsp0Ceil(SB)
	JA	eret_rsp0_overshoot	// over-retire — halt BEFORE the trample, with context
	MOVQ	AX, ·tssBuffer+4(SB)
	INCQ	·rsp0EretAddCount(SB)	// accounting (see RSPCT in syscall_amd64.go)
	JMP	eret_rsp0_done
eret_rsp0_overshoot:
	MOVQ	$1, R14			// site code 1 = exception_return (halting; R14 free)
	JMP	rsp0_overshoot_dump(SB)
eret_rsp0_done:

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

	// MAZ-136 IRETQ guard: a kernel-CS return must land inside kernel .text.
	// The frame-path corruption (resume into dead exception-stack memory)
	// never touches a ThreadContext, so the Go-level badResumeRIP guard
	// cannot see it — this is the last instant the bad RIP is data.
	// All GPRs hold interrupted values: AX is pushed (frame reads offset
	// +8) and restored on the OK path. Flags are free — IRETQ reloads
	// RFLAGS from the frame. kernelTextHi==0 → bounds not yet published
	// (early boot) → skip.
	PUSHQ	AX
	CMPQ	16(SP), $0x08		// CS (8(SP) before the push)
	JNE	eret_guard_ok		// user return: any RIP is plausible
	MOVQ	·kernelTextHi(SB), AX
	TESTQ	AX, AX
	JZ	eret_guard_ok		// bounds not initialized yet
	CMPQ	8(SP), AX		// RIP >= etext?
	JAE	eret_guard_bad
	MOVQ	·kernelTextLo(SB), AX
	CMPQ	8(SP), AX		// RIP >= text?
	JAE	eret_guard_ok
eret_guard_bad:
	LEAQ	8(SP), R12		// R12 = &iretq 5-tuple (skip pushed AX)
	JMP	bad_iretq_dump(SB)	// prints frame + context, halts
eret_guard_ok:
	POPQ	AX
	IRETQ

// ============================================================================
// Load new thread context and IRETQ
// ============================================================================
// R12 = pointer to ThreadContext
load_context_and_iretq:
	// FRAME-RESTORE-BEGIN load-context-iretq
	// Validate CS from context before building IRETQ frame
	FLD(R12, ThreadContext_CS, R13)
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

	// IST ROTATION (MAZ-136 rev B): retire the ABANDONING handler's own
	// reservation. Every path into load_context_and_iretq is a context
	// switch made FROM INSIDE an exception handler (syscall-exit switch,
	// sigreturn, #PF block/switch) — that handler's chain dies right here,
	// and its exception_return (which would have done this ADD) never runs.
	// Any SHALLOWER suspended chains — this thread's or another's — keep
	// their reservations: the cursor is GLOBAL (top − stride × live chains)
	// and a suspended chain's stride is released only when it resumes and
	// returns. ‼ Do NOT "restore", reset, or install any per-context value
	// into TSS.IST1 here — the IST half is ONE shared region, and a
	// per-thread reset reuses it over another thread's suspended live chain
	// (the KVM-run-4 trample). See the IST ROTATION banner at
	// common_exception_entry. Same gate as entry/return. AX is scratch
	// (reloaded from ctx before IRETQ).
	MOVQ	·ist1Floor(SB), AX
	TESTQ	AX, AX
	JZ	lc_ist_done		// rotation not armed (early boot)
	MOVQ	·tssBuffer+36(SB), AX
	ADDQ	$const_istRotateStride, AX	// retire the dying level
	CMPQ	AX, ·ist1Ceil(SB)
	JA	lc_ist_overshoot	// over-retire — see exception_return's twin check
	MOVQ	AX, ·tssBuffer+36(SB)
	INCQ	·istLcAddCount(SB)	// accounting (see ISTCT in syscall_amd64.go)
	JMP	lc_ist_done
lc_ist_overshoot:
	MOVQ	$2, R14			// site code 2 = load_context_and_iretq (halting)
	JMP	ist_overshoot_dump(SB)
lc_ist_done:

	// RSP0 ROTATION (MAZ-136): retire the ABANDONING handler's RSP0
	// reservation — the second cursor's twin of the IST ADD above; the same
	// "shallower suspended chains keep their reservations" argument is what
	// protects the parked SyscallWaitSoftIRQ chain this rotation exists for.
	// ‼ Do NOT "restore", reset, or install any per-context value into
	// TSS.RSP0 here — the RSP0 half is ONE shared region (see the RSP0
	// ROTATION banner at common_exception_entry). Same gate as entry/return.
	// AX is scratch (reloaded from ctx before IRETQ).
	MOVQ	·rsp0Floor(SB), AX
	TESTQ	AX, AX
	JZ	lc_rsp0_done		// rotation not armed (early boot)
	MOVQ	·tssBuffer+4(SB), AX
	ADDQ	$const_rsp0RotateStride, AX	// retire the dying level
	CMPQ	AX, ·rsp0Ceil(SB)
	JA	lc_rsp0_overshoot	// over-retire — see exception_return's twin check
	MOVQ	AX, ·tssBuffer+4(SB)
	INCQ	·rsp0LcAddCount(SB)	// accounting (see RSPCT in syscall_amd64.go)
	JMP	lc_rsp0_done
lc_rsp0_overshoot:
	MOVQ	$2, R14			// site code 2 = load_context_and_iretq (halting)
	JMP	rsp0_overshoot_dump(SB)
lc_rsp0_done:

	// Restore FS_BASE from context (per-thread TLS base).
	// Without this, userspace threads that called arch_prctl(ARCH_SET_FS)
	// would resume with the wrong FS_BASE, causing TLS reads to return
	// garbage and crashing the Go runtime.
	//
	// CRITICAL: If FSBase==0 (e.g., kernel Thread 0 before any TLS setup),
	// skip BOTH the WRMSR and the TLS sync write. Otherwise the TLS sync
	// reads the stale FS_BASE from the previous thread and writes g to
	// a user-space address, corrupting user heap data (allgs corruption).
	FLD(R12, ThreadContext_FSBase, AX)
	TESTQ	AX, AX
	JZ	skip_fsbase_and_tls	// FSBase==0 → skip WRMSR AND TLS sync
	MOVQ	AX, DX
	SHRQ	$32, DX
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	WRMSR				// Restore FS_BASE

	// Sync TLS: write the thread's SAVED TLS-g to the TLS slot.
	// TLS layout on Linux amd64: g is at FS_BASE - 8 (i.e. FS:-8).
	// MAZ-135: restore context.TLSG (offset 424) — the g-home as it actually was
	// at save time — NOT context.R14. amd64 keeps g in two homes (R14 + TLS), and
	// `systemstack`'s exit can leave R14 stale (=g0) while TLS-g is correct
	// (=curg). Forcing TLS-g = R14 here would propagate the stale g0 into the TLS
	// home → `morestack on g0`. Restoring the saved TLS-g keeps both homes
	// faithful to the captured state (mirrors ARM64's verbatim single-home restore).
	MOVL	$0xC0000100, CX		// MSR_FS_BASE
	RDMSR				// EAX=low32, EDX=high32
	SHLQ	$32, DX
	ORQ	DX, AX			// RAX = FS_BASE
	FLD(R12, ThreadContext_TLSG, DX)	// DX = saved TLS-g (ctx.TLSG, the faithful dual-home restore)
	TESTQ	DX, DX
	JZ	skip_fsbase_and_tls	// g==0 → skip TLS write (user TLS page may not be faulted in yet),
					// matching RunFirstThread / YieldToReadyThread (CodeRabbit, PR #70)
	MOVQ	DX, -8(AX)		// Write saved TLS-g to TLS slot
skip_fsbase_and_tls:

	// Use the context's RSP to build the IRETQ frame
	// We need a scratch stack. Use exception stack from PerCPU.
	// For now, just build on current stack after adjusting.

	// Clear the frame and build fresh IRETQ frame
	FLD(R12, ThreadContext_RSP, AX)		// new RSP
	FLD(R12, ThreadContext_RFLAGS, BX)	// new RFLAGS
	// Force IF=1 in RFLAGS for threads restored after boot.
	// During early boot (kmazarinSyscallReady=0), threads inherit IF=0 to
	// prevent APIC timer interrupts before IDT/LAPIC are ready. After boot,
	// ALL threads (kernel AND user) must run with IF=1 so timer preemption
	// works. Without this, kernel clone children (sysmon, templateThread)
	// created during init with IF=0 run forever without timer interrupts,
	// starving thread 0 and halting the system.
	FLD(R12, ThreadContext_CS, R13)		// CS from context
	CMPQ	R13, $0x1B		// userCS?
	JE	rflags_force_if		// User thread: always force IF=1
	// Kernel thread: force IF=1 only after boot
	MOVL	runtime·kmazarinSyscallReady(SB), R13
	TESTL	R13, R13
	JZ	rflags_no_fix		// Early boot: trust inherited IF state
rflags_force_if:
	ORQ	$0x200, BX		// Force IF=1
rflags_no_fix:
	FLD(R12, ThreadContext_RIP, CX)		// new RIP

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
	// MAZ-136 IRETQ guard: a kernel-CS context must resume inside kernel
	// .text. Covers the asm paths that JMP here without passing the Go-level
	// badResumeRIP guard (sigreturn, syscall-exit DoContextSwitch). CX = new
	// RIP; R13 is scratch (reloaded below). kernelTextHi==0 → bounds not yet
	// published (early boot) → skip.
	MOVQ	·kernelTextHi(SB), R13
	TESTQ	R13, R13
	JZ	ctx_guard_ok		// bounds not initialized yet
	CMPQ	ThreadContext_CS(R12), $0x08
	JNE	ctx_guard_ok		// user context: any RIP is plausible
	CMPQ	CX, R13			// RIP >= etext?
	JAE	ctx_guard_bad
	MOVQ	·kernelTextLo(SB), R13
	CMPQ	CX, R13			// RIP >= text?
	JAE	ctx_guard_ok
ctx_guard_bad:
	JMP	bad_ctx_dump(SB)	// R12 = ctx; prints context, halts
ctx_guard_ok:

	// Find a safe SP location (below current frame)
	LEAQ	-48(SP), SP		// Make room

	// Build IRETQ frame with CS/SS from ThreadContext
	FLD(R12, ThreadContext_SS, R13)		// SS from context
	MOVQ	R13, 32(SP)		// SS in IRETQ frame
	MOVQ	AX, 24(SP)		// RSP
	MOVQ	BX, 16(SP)		// RFLAGS
	FLD(R12, ThreadContext_CS, R13)		// CS from context
	MOVQ	R13, 8(SP)		// CS in IRETQ frame
	MOVQ	CX, 0(SP)		// RIP

	// Restore XMM registers from per-thread ctx.XMM. SaveContextFromFrame copied
	// xmmSaveArea → ctx.XMM when the thread was last saved, so this restores the
	// correct per-thread XMM state.
	MOVOU	ThreadContext_XMM+0(R12), X0
	MOVOU	ThreadContext_XMM+16(R12), X1
	MOVOU	ThreadContext_XMM+32(R12), X2
	MOVOU	ThreadContext_XMM+48(R12), X3
	MOVOU	ThreadContext_XMM+64(R12), X4
	MOVOU	ThreadContext_XMM+80(R12), X5
	MOVOU	ThreadContext_XMM+96(R12), X6
	MOVOU	ThreadContext_XMM+112(R12), X7
	MOVOU	ThreadContext_XMM+128(R12), X8
	MOVOU	ThreadContext_XMM+144(R12), X9
	MOVOU	ThreadContext_XMM+160(R12), X10
	MOVOU	ThreadContext_XMM+176(R12), X11
	MOVOU	ThreadContext_XMM+192(R12), X12
	MOVOU	ThreadContext_XMM+208(R12), X13
	MOVOU	ThreadContext_XMM+224(R12), X14
	MOVOU	ThreadContext_XMM+240(R12), X15

	// Load GPRs from ctx via the shared CTX_GPRS list; R12 (the base pointer) is
	// loaded last, outside the list.
	CTX_GPRS(FLD, R12)
	FLD(R12, ThreadContext_R12, R12)	// Load R12 last
	// FRAME-RESTORE-END

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
// MAZ-136 IRETQ-level resume-guard dumps (raw COM1, then halt forever)
// ============================================================================
// The frame-path corruption resumes the kernel into dead exception-stack
// memory without the bad RIP ever appearing in a ThreadContext, so only a
// check at the IRETQ itself can catch the corrupt frame intact. klog is
// FORBIDDEN here (IRQ-off, arbitrary nesting depth); pf_print_hex16 only.

// bad_iretq_dump — a kernel-CS iretq frame's RIP is outside kernel .text.
// In: R12 = &frame 5-tuple [RIP CS RFLAGS RSP SS]. Never returns.
// Prints the 5-tuple, vector/svcDepth/TLS-g, the frame address, then
// LO= the 16 qwords below the frame (the just-popped GPR save area, in
// pop order: AX BX CX DX SI DI BP R8..R15 errcode — R14 slot = the g)
// and HI= the 8 qwords above it (the interrupted stack = nesting context).
TEXT bad_iretq_dump(SB), NOSPLIT|NOFRAME, $0
	LEAQ	-0x200(R12), SP		// scratch stack clear of the dump window
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'A', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'E', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'Q', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	0(R12), R15		// RIP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	8(R12), R15		// CS
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	16(R12), R15		// RFLAGS
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	24(R12), R15		// RSP
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	32(R12), R15		// SS
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'G', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·savedExcKernelTLSG(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	R12, R15		// frame address (pre-IRETQ SP)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'L', AX; OUTB
	MOVB	$'O', AX; OUTB
	MOVB	$'=', AX; OUTB
	LEAQ	-128(R12), R14		// base of the popped GPR save area
	XORQ	BX, BX
bad_iq_lo:
	MOVQ	(R14)(BX*1), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	ADDQ	$8, BX
	CMPQ	BX, $128
	JL	bad_iq_lo
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'H', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	LEAQ	40(R12), R14		// first qword past the 5-tuple
	XORQ	BX, BX
bad_iq_hi:
	MOVQ	(R14)(BX*1), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	ADDQ	$8, BX
	CMPQ	BX, $64
	JL	bad_iq_hi
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
bad_iq_halt:
	HLT
	JMP	bad_iq_halt

// bad_ctx_dump — a kernel-CS ThreadContext's RIP is outside kernel .text.
// In: R12 = &ThreadContext. Never returns. Shared by load_context_and_iretq
// and the context restores in abi_stubs_amd64.s (RunFirstThread,
// YieldToReadyThread). Uses the caller's SP (valid in all three).
TEXT bad_ctx_dump(SB), NOSPLIT|NOFRAME, $0
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'A', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_RIP, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_CS, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_RSP, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'B', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_RBP, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'G', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_R14, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'G', AX; OUTB
	MOVB	$'=', AX; OUTB
	FLD(R12, ThreadContext_TLSG, R15)
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'X', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	R12, R15		// the context pointer itself
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·CurrentThread(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
bad_ctx_halt:
	HLT
	JMP	bad_ctx_halt

// ist_overflow_dump — the IST rotation would lower TSS.IST1 below ist1Floor:
// exception nesting exceeded the 8 reserved stride levels. Either something
// is recursively faulting, or a new exit path was added without its balancing
// ADD / absolute restore (see the IST ROTATION banner in
// common_exception_entry) and the leak finally hit the floor. Halting here is
// the POINT — the alternative is the silent stack trample this scheme exists
// to prevent. Prints vector, svcDepth, the live IST1, the floor, and SP,
// then halts. Never returns; registers are free.
TEXT ist_overflow_dump(SB), NOSPLIT|NOFRAME, $0
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'O', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+36(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·ist1Floor(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	SP, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
ist_ovf_halt:
	HLT
	JMP	ist_ovf_halt

// ist_overshoot_dump — an IST-rotation ADD would raise the cursor ABOVE the
// IST1-half top (ist1Ceil): an OVER-RETIRE. Some exit path released a
// reservation belonging to a still-live chain; the next IST delivery would
// land on top of that chain (the MAZ-136 trample). Halting here catches the
// guilty exit in the act, with its site code and vector, BEFORE the trample.
// In: R14 = site code (1 = exception_return, 2 = load_context_and_iretq).
// Prints site, vector, svcDepth, the live cursor, the ceiling, and SP, then
// halts. Never returns; registers are free.
TEXT ist_overshoot_dump(SB), NOSPLIT|NOFRAME, $0
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'T', AX; OUTB
	MOVB	$'O', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	R14, R15		// site code
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'I', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+36(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·ist1Ceil(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	SP, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
ist_ovr_halt:
	HLT
	JMP	ist_ovr_halt

// rsp0_overflow_dump — the RSP0 rotation would lower TSS.RSP0 below
// rsp0Floor: twin of ist_overflow_dump for the second cursor. Either
// exception nesting exceeded the 14 reserved levels, a single level broke
// the one-level-use bound (grew past rsp0RotateStride — see the RSP0
// ROTATION banner in common_exception_entry), or an exit path leaked strides
// until the drift hit the floor. Halting here is the POINT — the alternative
// is the silent parked-chain trample this scheme exists to prevent. Prints
// vector, svcDepth, the live RSP0, the floor, and SP, then halts. Never
// returns; registers are free.
TEXT rsp0_overflow_dump(SB), NOSPLIT|NOFRAME, $0
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'O', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+4(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'F', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·rsp0Floor(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	SP, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
rsp0_ovf_halt:
	HLT
	JMP	rsp0_ovf_halt

// rsp0_overshoot_dump — an RSP0-rotation ADD would raise the cursor ABOVE
// the RSP0-half top (rsp0Ceil): an OVER-RETIRE; twin of ist_overshoot_dump
// for the second cursor. Some exit path released a reservation belonging to
// a still-live chain — most dangerously a PARKED SyscallWaitSoftIRQ chain;
// the next ring3→ring0 delivery would land on top of it (the MAZ-136 run-45
// trample). Halting here catches the guilty exit in the act, BEFORE the
// trample. In: R14 = site code (1 = exception_return,
// 2 = load_context_and_iretq). Prints site, vector, svcDepth, the live
// cursor, the ceiling, and SP, then halts. Never returns; registers are free.
TEXT rsp0_overshoot_dump(SB), NOSPLIT|NOFRAME, $0
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'O', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$' ', AX; OUTB
	MOVB	$'S', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	R14, R15		// site code
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'V', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·currentVector(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'D', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVL	·svcDepth(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'R', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·tssBuffer+4(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'C', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	·rsp0Ceil(SB), R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$' ', AX; OUTB
	MOVB	$'P', AX; OUTB
	MOVB	$'=', AX; OUTB
	MOVQ	SP, R15
	CALL	pf_print_hex16(SB)
	MOVW	$0x3F8, DX
	MOVB	$'\n', AX; OUTB
	CLI
rsp0_ovr_halt:
	HLT
	JMP	rsp0_ovr_halt

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

	// Switch to the kernel exception stack — UNLESS the caller is already
	// on it. A ring-0 SYSCALL (Go runtime nanotime / rawSyscallNoError
	// fallbacks) issued from a handler chain resident on the exception
	// stack must NEST at the current SP: resetting to the fixed top
	// tramples the suspended chain, which then RETs through an overwritten
	// return-address slot into dead stack — the MAZ-136 deterministic
	// RIP=0xFFFFFFFF44125BF0 corruption. Callers on other stacks (user,
	// g0, goroutine) still switch for guaranteed dispatch headroom.
	// Mirrors ARM64: EL1t→EL1h switches to SP_EL1, EL1h→EL1h nests.
	// CX is free: the caller's CX is already in syscallScratchRCX and CX
	// is reloaded from the scratch globals below. Bounds zero until
	// InitThreads publishes them → both compares fail low → switch (the
	// pre-fix behavior; SYSCALLs cannot occur that early).
	MOVQ	SP, ·syscallScratchRSP(SB)
	MOVQ	·excStackBottom(SB), CX
	CMPQ	SP, CX
	JB	syscall_do_switch	// below the exception stack → switch
	MOVQ	·excStackTop(SB), CX
	CMPQ	SP, CX
	JB	syscall_keep_stack	// inside [excStackBottom, excStackTop) → nest in place
syscall_do_switch:
	// ‼ RSP0 ROTATION COUPLING (MAZ-136) — this switch MUST track the LIVE
	// cursor. SYSCALL switches no stacks in hardware, so ring-3 SYSCALLs
	// bypass the rotating TSS.RSP0 load entirely — this software switch is
	// their ONLY stack reset. Pointing it at a fixed top resurrects the
	// parked-chain trample the RSP0 rotation exists to fix (run 45:
	// execute-the-stack at RSP0top−0x410 over SyscallWaitSoftIRQ's parked
	// chain). Read TSS.RSP0 (·tssBuffer+4 — the cursor the rotation
	// maintains; see the RSP0 ROTATION banner in common_exception_entry),
	// NEVER excStackTopForSyscall, which is boot-window-only (threads.go).
	// Gate: before copyGDTToOwnedBuffer publishes rsp0Floor the cursor
	// bytes are 0 — fall back to the boot-window top (pre-rotation
	// behavior; common_exception_entry's SUB skips on the same gate, so
	// entry and exit stay balanced). CX is free here (see above).
	MOVQ	·rsp0Floor(SB), CX
	TESTQ	CX, CX
	JZ	syscall_switch_boot
	MOVQ	·tssBuffer+4(SB), SP	// live RSP0 cursor: land BELOW parked chains
	JMP	syscall_keep_stack
syscall_switch_boot:
	MOVQ	·excStackTopForSyscall(SB), SP	// boot window only (rsp0Floor==0)
syscall_keep_stack:

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



