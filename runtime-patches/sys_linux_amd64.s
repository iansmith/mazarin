// KMAZARIN OVERLAY: Stub out Linux syscalls for bare-metal x86_64 environment
//
// This file replaces runtime/sys_linux_amd64.s
// It provides stub implementations for a bare-metal kernel environment.
// Kmazarin runs on bare metal with no underlying OS, so all "syscalls"
// are routed through kmazarin's IDT handler via INT $0x80.
//
// Syscall numbers here use native x86_64 Linux numbers. INT $0x80 delivers
// them to kmazarin's exception handler, which translates via SysID dispatch.
//
// Key differences from ARM64 kmazarin overlay:
// - Uses OUTB to COM1 (port 0x3F8) instead of MMIO UART
// - Uses RDTSC instead of CNTVCT_EL0 for timers
// - Uses INT $0x80 instead of SVC (both caught by kmazarin's exception handler)
// - Uses HLT instead of WFI for halt loops
// - TLS via FS segment (WRMSR to MSR_FS_BASE) instead of R28

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_amd64.h"

#define COM1_PORT $0x3F8

// Syscall numbers — native x86_64 Linux convention.
// INT $0x80 delivers these to kmazarin's dispatcher which translates to SysID.
#define SYS_exit         60
#define SYS_exit_group   231
#define SYS_futex        202
#define SYS_sched_yield  24
#define SYS_gettid       186
#define SYS_clone        56

// kmazarinUART - write a byte to COM1 via port I/O
// func kmazarinUART(c byte)
TEXT runtime·kmazarinUART(SB),NOSPLIT,$0-1
	MOVBLZX	c+0(FP), AX
	MOVW	COM1_PORT, DX
	OUTB
	RET

// exit - print diagnostic then halt loop (no OS to exit to)
TEXT runtime·exit(SB),NOSPLIT,$0-4
	// Write 'E' to COM1
	MOVW	COM1_PORT, DX
	MOVB	$'E', AX
	OUTB
	// Print exit code as 2 hex digits
	MOVL	code+0(FP), CX
	MOVL	CX, AX
	SHRL	$4, AX
	ANDL	$0xF, AX
	CMPL	AX, $10
	JB	exit_dig1
	ADDL	$('A'-10), AX
	JMP	exit_out1
exit_dig1:
	ADDL	$'0', AX
exit_out1:
	OUTB
	MOVL	CX, AX
	ANDL	$0xF, AX
	CMPL	AX, $10
	JB	exit_dig2
	ADDL	$('A'-10), AX
	JMP	exit_out2
exit_dig2:
	ADDL	$'0', AX
exit_out2:
	OUTB
halt_loop:
	HLT
	JMP	halt_loop

// exitThread - store 0 at wait addr, then halt
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	MOVQ	wait+0(FP), AX
	MOVL	$0, (AX)
exit_thread_loop:
	HLT
	JMP	exit_thread_loop

// open - not supported, return -1
TEXT runtime·open(SB),NOSPLIT,$0-20
	MOVL	$-1, ret+16(FP)
	RET

// closefd - no-op, return 0
TEXT runtime·closefd(SB),NOSPLIT,$0-12
	MOVL	$0, ret+8(FP)
	RET

// write1 - write bytes to COM1 serial port
// func write1(fd uintptr, p unsafe.Pointer, n int32) int32
TEXT runtime·write1(SB),NOSPLIT,$0-28
	MOVQ	p+8(FP), SI		// buffer pointer
	MOVL	n+16(FP), CX		// byte count
	MOVW	COM1_PORT, DX		// COM1 port
	TESTL	CX, CX
	JZ	write1_done
write1_loop:
	MOVB	(SI), AX
	OUTB
	INCQ	SI
	DECL	CX
	JNZ	write1_loop
write1_done:
	MOVL	n+16(FP), AX
	MOVL	AX, ret+24(FP)
	RET

// read - not supported, return -1
TEXT runtime·read(SB),NOSPLIT,$0-28
	MOVL	$-1, ret+24(FP)
	RET

// pipe2 - not supported
TEXT runtime·pipe2(SB),NOSPLIT,$0-20
	MOVL	$-1, errno+16(FP)
	RET

// usleep - yield CPU via sched_yield INT $0x80
// Kmazarin has no timer interrupts during early boot, so spinning would
// block forever if another thread is on the ready queue.
// Using sched_yield lets the scheduler run queued threads.
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	MOVL	$SYS_sched_yield, AX
	INT	$0x80
	RET

// gettid - return 1
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOVL	$1, ret+0(FP)
	RET

// raise - no-op
TEXT runtime·raise(SB),NOSPLIT,$0
	RET

// raiseproc - no-op
TEXT runtime·raiseproc(SB),NOSPLIT,$0
	RET

// getpid - return 1
TEXT ·getpid(SB),NOSPLIT,$0-8
	MOVQ	$1, ret+0(FP)
	RET

// tgkill - no-op
TEXT ·tgkill(SB),NOSPLIT,$0
	RET

// setitimer - no-op
TEXT runtime·setitimer(SB),NOSPLIT,$0-24
	RET

// timer_create - return -1
TEXT runtime·timer_create(SB),NOSPLIT,$0-28
	MOVL	$-1, ret+24(FP)
	RET

// timer_settime - return -1
TEXT runtime·timer_settime(SB),NOSPLIT,$0-28
	MOVL	$-1, ret+24(FP)
	RET

// timer_delete - return -1
TEXT runtime·timer_delete(SB),NOSPLIT,$0-12
	MOVL	$-1, ret+8(FP)
	RET

// mincore - return -1
TEXT runtime·mincore(SB),NOSPLIT,$0-28
	MOVL	$-1, ret+24(FP)
	RET

// walltime - derive from RDTSC.
// Returns (seconds, nanoseconds) since an arbitrary epoch.
// Assumes ~2 GHz TSC frequency (approximate, good enough for monotonic clock).
// 2^31 ticks / 2 GHz ≈ 1 second, so shift right 31 for seconds.
TEXT runtime·walltime(SB),NOSPLIT,$0-12
	RDTSC
	SHLQ	$32, DX
	ADDQ	DX, AX		// AX = full 64-bit TSC value
	// Approximate: seconds = TSC >> 31 (assumes ~2 GHz)
	MOVQ	AX, CX
	SHRQ	$31, CX		// CX ≈ seconds
	MOVQ	CX, sec+0(FP)
	// Remainder: AX - (CX << 31), then approximate nanoseconds
	SHLQ	$31, CX
	SUBQ	CX, AX		// AX = remainder ticks
	// Convert remaining ticks to nanoseconds: ns ≈ ticks / 2
	SHRQ	$1, AX
	MOVL	AX, nsec+8(FP)
	RET

// nanotime1 - read TSC, convert to approximate nanoseconds.
// Assumes ~2 GHz TSC → multiply by ~0.5 ns/tick (shift right by 1).
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	RDTSC
	SHLQ	$32, DX
	ADDQ	DX, AX		// AX = full 64-bit TSC
	// Approximate ns: each tick ≈ 0.5 ns at 2 GHz
	SHRQ	$1, AX
	MOVQ	AX, ret+0(FP)
	RET

// rtsigprocmask - no-op
TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	RET

// rt_sigaction - return 0
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	MOVL	$0, ret+32(FP)
	RET

// callCgoSigaction - no-op, return 0
TEXT runtime·callCgoSigaction(SB),NOSPLIT,$16
	MOVL	$0, ret+24(FP)
	RET

// sigfwd - no-op
TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	RET

// sigtramp - no-op
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME|NOFRAME,$0
	RET

// sigprofNonGoWrapper - no-op
TEXT runtime·sigprofNonGoWrapper<>(SB),NOSPLIT|NOFRAME,$0
	RET

// cgoSigtramp - no-op
TEXT runtime·cgoSigtramp(SB),NOSPLIT,$0
	RET

// sigreturn__sigaction - no-op
TEXT runtime·sigreturn__sigaction(SB),NOSPLIT,$0
	RET

// sysMmap - return error (handled by Go-level cgo_mmap overlay)
TEXT runtime·sysMmap(SB),NOSPLIT,$0
	MOVQ	$0, p+32(FP)
	MOVQ	$12, err+40(FP)	// ENOMEM
	RET

// callCgoMmap - not supported
TEXT runtime·callCgoMmap(SB),NOSPLIT,$16
	MOVQ	$-1, ret+32(FP)
	RET

// sysMunmap - no-op
TEXT runtime·sysMunmap(SB),NOSPLIT,$0
	RET

// callCgoMunmap - no-op
TEXT runtime·callCgoMunmap(SB),NOSPLIT,$16-16
	RET

// madvise - no-op, return 0
TEXT runtime·madvise(SB),NOSPLIT,$0
	MOVL	$0, ret+24(FP)
	RET

// futex - implement FUTEX_WAIT with spin-check, FUTEX_WAKE as immediate return.
//
// For FUTEX_WAIT (op & 0x7F == 0): check if *addr == val. If not, return
// EAGAIN (-11). If yes, spin briefly checking for value change, then
// yield to scheduler and return 0 (simulating timeout/spurious wakeup).
//
// For FUTEX_WAKE (op & 0x7F == 1): return 0 immediately.
//
// func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 unsafe.Pointer, val3 uint32) int32
TEXT runtime·futex(SB),NOSPLIT,$0
	MOVQ	addr+0(FP), DI		// addr
	MOVL	op+8(FP), SI		// op
	MOVL	val+12(FP), DX		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	MOVL	SI, AX
	ANDL	$0x7F, AX
	CMPL	AX, $1
	JEQ	futex_wake		// FUTEX_WAKE

	// FUTEX_WAIT: check *addr vs val
	MOVL	(DI), AX		// *addr
	CMPL	AX, DX
	JNE	futex_eagain		// *addr != val → return EAGAIN

	// *addr == val: spin briefly waiting for change (128 iterations)
	MOVL	$128, CX
futex_spin:
	PAUSE
	MOVL	(DI), AX
	CMPL	AX, DX
	JNE	futex_eagain		// Value changed → return EAGAIN
	DECL	CX
	JNZ	futex_spin

	// Spin exhausted, value didn't change. Yield to scheduler so other
	// threads can run (no timer interrupts during early boot), then return
	// 0 to simulate a timeout/spurious wakeup.
	MOVL	$SYS_sched_yield, AX
	INT	$0x80
	MOVL	$0, ret+40(FP)
	RET

futex_eagain:
	// Value at addr differs from expected val → EAGAIN
	MOVL	$-11, ret+40(FP)	// EAGAIN = 11 on Linux
	RET

futex_wake:
	// FUTEX_WAKE: return 0 (nothing to wake on bare metal)
	MOVL	$0, ret+40(FP)
	RET

// clone - issue INT $0x80 so kmazarin's IDT handler can create a real thread.
// This mirrors the standard Go runtime clone but uses INT $0x80 which is
// caught by kmazarin's exception handler (vector 128).
//
// On x86_64 INT $0x80 convention: syscall number in RAX, args in RDI, RSI, RDX, R10, R8, R9.
//
// int64 clone(int32 flags, void *stk, M *mp, G *gp, void (*fn)(void));
TEXT runtime·clone(SB),NOSPLIT|NOFRAME,$0
	MOVL	flags+0(FP), DI
	MOVQ	stk+8(FP), SI

	// Copy mp, gp, fn off parent stack for use by child.
	// These callee-saved registers survive the INT $0x80 and are inherited
	// by the child via CloneNeedsParentRegs in the exception handler.
	MOVQ	mp+16(FP), R13
	MOVQ	gp+24(FP), R9
	MOVQ	fn+32(FP), R12

	// Write canary on child stack for integrity check
	MOVQ	$1234, -32(SI)

	// Clear unused syscall args
	MOVQ	$0, DX		// ptid
	MOVQ	$0, R10		// ctid

	// Set up newtls for CLONE_SETTLS — the child needs its own FS_BASE
	// so writing g to FS:-8 goes to the child's m.tls[0], not the parent's.
	// This matches the standard Go runtime (sys_linux_amd64.s:577-584).
	CMPQ	R13, $0
	JEQ	clone_no_tls
	CMPQ	R9, $0
	JEQ	clone_no_tls
	LEAQ	m_tls(R13), R8
	ADDQ	$8, R8		// R8 = &m.tls[1], so FS:-8 = m.tls[0] (g slot)
	ORQ	$0x00080000, DI	// add CLONE_SETTLS to flags
	JMP	clone_do_syscall
clone_no_tls:
	MOVQ	$0, R8
clone_do_syscall:

	MOVL	$SYS_clone, AX
	INT	$0x80

	// In parent, return.
	CMPQ	AX, $0
	JEQ	clone_child
	MOVL	AX, ret+40(FP)
	RET

clone_child:
	// In child, on new stack.
	MOVQ	SI, SP

	// Check canary — if overwritten, the IRET frame or timer interrupt
	// clobbered the child stack (see IF=1 fix in load_context_and_iretq).
	CMPQ	-32(SP), $1234
	JEQ	clone_good
	INT	$3		// canary corrupted — crash for debugging

clone_good:
	// R12 (fn), R9 (gp), R13 (mp) already have correct values from the parent
	// via CloneNeedsParentRegs (which copies all parent GPRs to the child context).

	// Initialize m->procid to Linux tid
	SUBQ	$48, SP
	MOVL	$SYS_gettid, AX
	INT	$0x80
	ADDQ	$48, SP

	CMPQ	R13, $0		// m
	JEQ	clone_nog
	CMPQ	R9, $0		// g
	JEQ	clone_nog

	MOVQ	AX, m_procid(R13)

	// Set up child TLS: g.m = mp, TLS.g = gp, R14 = gp
	get_tls(CX)
	MOVQ	R13, g_m(R9)
	MOVQ	R9, g(CX)
	MOVQ	R9, R14		// set g register

clone_nog:
	// Call fn. This is the PC of an ABI0 function.
	CALL	R12

	// It shouldn't return. If it does, exit that thread.
	MOVL	$111, DI
	MOVL	$SYS_exit, AX
	INT	$0x80
	JMP	-3(PC)		// keep exiting

// sigaltstack - no-op
TEXT runtime·sigaltstack(SB),NOSPLIT,$0
	RET

// settls - set tls base to DI using WRMSR (bare-metal, no arch_prctl)
// Called from rt0_go with the TLS base address in DI (NOT on the stack).
// This matches the standard Go runtime convention: "set tls base to DI".
TEXT runtime·settls(SB),NOSPLIT,$32
	ADDQ	$8, DI		// ELF wants to use -8(FS)
	MOVQ	DI, AX
	MOVQ	DI, DX
	SHRQ	$32, DX		// DX = upper 32 bits
	MOVL	$0xC0000100, CX	// MSR_FS_BASE
	WRMSR
	RET

// osyield - yield with PAUSE (with diagnostic heartbeat to COM1)
TEXT runtime·osyield(SB),NOSPLIT,$0
	MOVW	COM1_PORT, DX
	MOVB	$'y', AX
	OUTB
	PAUSE
	RET

// sched_getaffinity - return 0
TEXT runtime·sched_getaffinity(SB),NOSPLIT,$0
	MOVL	$0, ret+24(FP)
	RET

// access - return -1
TEXT runtime·access(SB),NOSPLIT,$0
	MOVL	$-1, ret+16(FP)
	RET

// connect - return -1
TEXT runtime·connect(SB),NOSPLIT,$0-28
	MOVL	$-1, ret+24(FP)
	RET

// socket - return -1
TEXT runtime·socket(SB),NOSPLIT,$0-20
	MOVL	$-1, ret+16(FP)
	RET

// sbrk0 - return 0
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	MOVQ	$0, ret+0(FP)
	RET

// vgetrandom1 - return -1 (no random source)
TEXT runtime·vgetrandom1<ABIInternal>(SB),NOSPLIT,$0
	MOVQ	$-1, AX
	RET
