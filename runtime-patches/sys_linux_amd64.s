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

// exit - terminate entire process (all threads of this shepherd)
// Go runtime calls this for os.Exit() and runtime.throw() — it means
// "kill the whole process", matching Linux exit_group(2) semantics.
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
	// Call kernel exit_group to terminate all threads of this shepherd
	MOVL	code+0(FP), DI		// arg0: exit status
	MOVL	$SYS_exit_group, AX	// syscall 231 (terminate whole process)
	INT	$0x80
	// Should not return — kernel terminates shepherd and switches away
halt_loop:
	HLT
	JMP	halt_loop

// exitThread - signal thread exit to runtime, then call kernel exit syscall
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	MOVQ	wait+0(FP), AX
	MOVL	$0, (AX)		// Signal to runtime that thread is exiting
	XORL	DI, DI			// exit status 0
	MOVL	$SYS_exit, AX		// syscall 60
	INT	$0x80
	// Should not return
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
// When suppressSerial is set (SoftIRQ console active), skip UART output.
TEXT runtime·write1(SB),NOSPLIT,$0-28
	MOVL	runtime·suppressSerial(SB), AX
	TESTL	AX, AX
	JNZ	write1_suppressed
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
write1_suppressed:
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

// usleep - two-phase: spin+yield during early boot, INT $0x80 nanosleep post-boot.
//
// Phase 1 (early boot, kmazarinSyscallReady=0): spin with PAUSE for a
// reasonable number of iterations, then yield. The spin is critical for
// x86_64 under TCG emulation: direct yields are expensive (~0.5ms each)
// and cause sysmon+parent to livelock if yield frequency is too high.
// By spinning first, we amortize the yield cost and let threads make progress.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real nanosleep via INT $0x80.
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	MOVL	runtime·kmazarinSyscallReady(SB), AX
	TESTL	AX, AX
	JNZ	usleep_real

	// Phase 1: spin then yield via INT $0x80 sched_yield.
	// Uses INT $0x80 (not kmazarinYieldImpl) for proper thread queue management.
	MOVL	$128, CX
usleep_spin:
	PAUSE
	DECL	CX
	JNZ	usleep_spin
	MOVL	$SYS_sched_yield, AX
	INT	$0x80
	RET

usleep_real:
	// Phase 2: convert microseconds to nanoseconds, call nanosleep via INT $0x80
	// Build timespec on stack: {tv_sec=0, tv_nsec=usec*1000}
	MOVL	usec+0(FP), AX		// AX = microseconds
	MOVQ	$1000, CX
	MULQ	CX			// RAX = nanoseconds (max ~10ms = 10M ns, fits 64-bit)
	SUBQ	$16, SP			// allocate timespec
	MOVQ	$0, 0(SP)		// tv_sec = 0
	MOVQ	AX, 8(SP)		// tv_nsec = usec * 1000
	MOVQ	SP, DI			// arg0: &timespec
	MOVQ	$0, SI			// arg1: NULL (remaining)
	MOVL	$35, AX			// SYS_nanosleep on x86_64
	INT	$0x80
	ADDQ	$16, SP
	RET

// gettid - get thread ID via INT $0x80 (routed to kmazarin's dispatcher)
// Two-phase: before threads are initialized, return 1 (safe default).
// After kmazarinSyscallReady is set, use real syscall.
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOVL	runtime·kmazarinSyscallReady(SB), AX
	TESTL	AX, AX
	JZ	gettid_early
	MOVL	$SYS_gettid, AX
	INT	$0x80
	MOVL	AX, ret+0(FP)
	RET
gettid_early:
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

// tgkill - send signal to specific thread via INT $0x80
TEXT ·tgkill(SB),NOSPLIT,$0-24
	MOVQ	tgid+0(FP), DI
	MOVQ	tid+8(FP), SI
	MOVQ	sig+16(FP), DX
	MOVL	$234, AX		// SYS_tgkill on x86_64
	INT	$0x80
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

// nsPerTickX256 — fixed-point (×256) nanoseconds per TSC tick.
// Default 128 = 0.5 ns/tick × 256 for ~2 GHz TSC.
// Updated by kmazarin's initTimerFrequency() when the real frequency
// is calibrated: nsPerTickX256 = 256000000000 / freq.
DATA	runtime·nsPerTickX256+0(SB)/8, $128
GLOBL	runtime·nsPerTickX256(SB), NOPTR, $8

// nanotime1 — monotonic nanoseconds from TSC.
// Reads nsPerTickX256 for a dynamic conversion factor:
//   ns = (ticks × nsPerTickX256) >> 8
// MULQ produces 128-bit RDX:RAX; extract bits [71:8] for correct >>8.
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	RDTSC
	SHLQ	$32, DX
	ADDQ	DX, AX			// AX = full 64-bit TSC
	MULQ	runtime·nsPerTickX256(SB) // RDX:RAX = TSC × nsPerTickX256
	SHRQ	$8, AX			// low 64 >> 8
	SHLQ	$56, DX			// high bits [7:0] → [63:56]
	ORQ	DX, AX			// combine: bits [71:8] of 128-bit product
	MOVQ	AX, ret+0(FP)
	RET

// rtsigprocmask - no-op
TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	RET

// rt_sigaction - register signal handler via INT $0x80.
// Always dispatched (no two-phase guard): diplomat installs the IDT before
// the Go runtime starts, so INT $0x80 is available during schedinit/initsig.
// The kmazarinSyscallReady guard was silently dropping all 56 kernel signal
// registrations, leaving signalActions[] empty (total=0 vs ARM64's total=56).
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	MOVL	sig+0(FP), DI		// arg0: signal number
	MOVQ	new+8(FP), SI		// arg1: new sigaction ptr
	MOVQ	old+16(FP), DX		// arg2: old sigaction ptr
	MOVQ	size+24(FP), R10	// arg3: sigset size
	MOVL	$13, AX			// SYS_rt_sigaction on x86_64
	INT	$0x80
	MOVL	AX, ret+32(FP)
	RET

// callCgoSigaction - no-op, return 0
TEXT runtime·callCgoSigaction(SB),NOSPLIT,$16
	MOVL	$0, ret+24(FP)
	RET

// sigfwd - no-op
TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	RET

// sigtramp - signal trampoline matching Go runtime's real implementation.
// Called when kmazarin delivers a signal: RDI=signum, RSI=siginfo, RDX=ucontext.
// Saves SysV callee-saved registers, sets up Go ABI, calls sigtrampgo.
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME|NOFRAME,$0
	// Save SysV callee-saved registers (match PUSH_REGS_HOST_TO_ABI0)
	ADJSP	$48
	MOVQ	BP, 40(SP)
	LEAQ	40(SP), BP
	MOVQ	BX, 0(SP)
	MOVQ	R12, 8(SP)
	MOVQ	R13, 16(SP)
	MOVQ	R14, 24(SP)
	MOVQ	R15, 32(SP)

	// Set up ABIInternal: g from TLS, clear X15
	get_tls(R12)
	MOVQ	g(R12), R14
	PXOR	X15, X15

	// Spill slots for ABIInternal call
	NOP	SP
	ADJSP	$24

	// Call sigtrampgo(sig, info, ctx)
	MOVQ	DI, AX		// sig
	MOVQ	SI, BX		// info
	MOVQ	DX, CX		// ctx
	CALL	·sigtrampgo<ABIInternal>(SB)

	ADJSP	$-24

	// Restore callee-saved
	MOVQ	0(SP), BX
	MOVQ	8(SP), R12
	MOVQ	16(SP), R13
	MOVQ	24(SP), R14
	MOVQ	32(SP), R15
	MOVQ	40(SP), BP
	ADJSP	$-48
	RET

// sigprofNonGoWrapper - no-op
TEXT runtime·sigprofNonGoWrapper<>(SB),NOSPLIT|NOFRAME,$0
	RET

// cgoSigtramp - no-op
TEXT runtime·cgoSigtramp(SB),NOSPLIT,$0
	RET

// sigreturn__sigaction - invoke rt_sigreturn via INT $0x80
TEXT runtime·sigreturn__sigaction(SB),NOSPLIT|NOFRAME,$0
	MOVL	$15, AX			// SYS_rt_sigreturn on x86_64
	INT	$0x80
	INT	$3			// Should not return

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

// madvise - stub, return 0
TEXT runtime·madvise(SB),NOSPLIT,$0
	MOVL	$0, ret+24(FP)
	RET

// futex - two-phase implementation:
//
// Phase 1 (early boot, before SetVBAR): spin-check + yield.
//   No kmazarin exception vectors installed, so only basic INT $0x80 works.
//   Spin briefly checking for value change, then yield.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real INT $0x80 with AX=202
//   (SYS_futex on x86_64). Routes through kmazarin's SyscallFutex handler
//   which properly blocks the thread (FUTEX_WAIT) or wakes blocked threads
//   (FUTEX_WAKE).
//
// func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 unsafe.Pointer, val3 uint32) int32
TEXT runtime·futex(SB),NOSPLIT,$0
	// Check if kmazarin syscall handlers are ready
	MOVL	runtime·kmazarinSyscallReady(SB), AX
	TESTL	AX, AX
	JNZ	futex_real

	// --- Phase 1: early boot (spin + yield) ---
	// Before kmazarinSyscallReady, INT $0x80 is still available — diplomat
	// installs kmazarin's ISR vectors in the IDT before jumping to kmazarin.
	// Use INT $0x80 with SYS_sched_yield (not kmazarinYieldImpl) because the
	// SVC exception handler → SyscallSchedYield → DoContextSwitch path properly
	// manages thread queues. kmazarinYieldImpl → SaveThread0AndYield doesn't
	// enqueue non-thread-0 threads, orphaning them.
	MOVQ	addr+0(FP), DI		// addr
	MOVL	op+8(FP), SI		// op
	MOVL	val+12(FP), DX		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	MOVL	SI, AX
	ANDL	$0x7F, AX
	CMPL	AX, $1
	JEQ	futex_wake_early	// FUTEX_WAKE

	// FUTEX_WAIT: check *addr vs val
	MOVL	(DI), AX		// *addr
	CMPL	AX, DX
	JNE	futex_eagain		// *addr != val → return EAGAIN

	// *addr == val: spin waiting for change.
	MOVL	$128, CX
futex_spin:
	PAUSE
	MOVL	(DI), AX
	CMPL	AX, DX
	JNE	futex_eagain		// Value changed → return EAGAIN
	DECL	CX
	JNZ	futex_spin

	// Spin exhausted, value didn't change. Yield via INT $0x80 sched_yield
	// so the exception handler can properly context-switch to another thread.
	MOVL	$SYS_sched_yield, AX
	INT	$0x80
	MOVL	$0, ret+40(FP)
	RET

futex_eagain:
	// Value at addr differs from expected val → EAGAIN
	MOVL	$-11, ret+40(FP)	// EAGAIN = 11 on Linux
	RET

futex_wake_early:
	// FUTEX_WAKE: return 0 (nothing to wake on bare metal)
	MOVL	$0, ret+40(FP)
	RET

futex_real:
	// --- Phase 2: real futex INT $0x80 (post-boot) ---
	// Route through kmazarin's SyscallFutex handler for proper blocking/waking.
	// x86_64 INT $0x80 convention: AX=syscall#, DI, SI, DX, R10, R8, R9
	MOVQ	addr+0(FP), DI		// uaddr
	MOVL	op+8(FP), SI		// op
	MOVL	val+12(FP), DX		// val
	MOVQ	ts+16(FP), R10		// timeout
	MOVQ	addr2+24(FP), R8	// uaddr2
	MOVL	val3+32(FP), R9		// val3
	MOVL	$202, AX		// SYS_futex on x86_64
	INT	$0x80
	MOVL	AX, ret+40(FP)
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

// sigaltstack - set/get signal stack via INT $0x80.
// Always dispatched (no two-phase guard), matching rt_sigaction.
TEXT runtime·sigaltstack(SB),NOSPLIT,$0-16
	MOVQ	new+0(FP), DI
	MOVQ	old+8(FP), SI
	MOVL	$131, AX		// SYS_sigaltstack on x86_64
	INT	$0x80
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

// osyield - yield with PAUSE
TEXT runtime·osyield(SB),NOSPLIT,$0
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
