// KMAZARIN OVERLAY: Stub out Linux syscalls for bare-metal RISC-V environment
//
// This file replaces runtime/sys_linux_riscv64.s
// It provides stub implementations that don't use ECALL instructions.
// On RISC-V, ECALL from S-mode goes to M-mode (OpenSBI), NOT to our trap handler.
// Instead, we use EBREAK (breakpoint, scause=3) which IS delegated to S-mode
// via medeleg bit 3. The trap handler catches EBREAK and dispatches via A7.
//
// mmap/munmap are provided by cgo_mmap.go (bump allocator, no syscall needed).

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"

// exit - terminate entire process (all threads of this shepherd)
// Go runtime calls this for os.Exit() and runtime.throw() — it means
// "kill the whole process", matching Linux exit_group(2) semantics.
TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
	MOVW	code+0(FP), A0		// arg0: exit status
	MOV	$94, A7			// SYS_exit_group (terminate all threads)
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	// Should not return — kernel terminates shepherd and switches away
halt_loop:
	WORD	$0x10500073		// wfi
	JMP	halt_loop

// exitThread - signal thread exit to runtime, then call kernel exit syscall
TEXT runtime·exitThread(SB),NOSPLIT|NOFRAME,$0-8
	MOV	wait+0(FP), A0
	MOV	ZERO, T0
	WORD	$0x1805352F		// amoswap.w.rl zero, zero, (a0) — store-release 0
	MOV	ZERO, A0		// exit status 0
	MOV	$93, A7			// SYS_exit
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	// Should not return
exit_thread_loop:
	WORD	$0x10500073		// wfi
	JMP	exit_thread_loop

// open - not supported, return -1
TEXT runtime·open(SB),NOSPLIT|NOFRAME,$0-20
	MOV	$-1, A0
	MOVW	A0, ret+16(FP)
	RET

// closefd - no-op, return 0
TEXT runtime·closefd(SB),NOSPLIT|NOFRAME,$0-12
	MOV	ZERO, A0
	MOVW	A0, ret+8(FP)
	RET

// write1 - write bytes to UART via linear map
// When suppressSerial is set (SoftIRQ console active), skip UART output.
TEXT runtime·write1(SB),NOSPLIT|NOFRAME,$0-28
	MOV	$runtime·suppressSerial(SB), T0
	MOVW	(T0), T1
	BNE	T1, ZERO, write1_suppressed
	// UART VA = 0xFFFFFFFF10000000 (NS16550 via linear map)
	MOV	$0xFFFFFFFF10000000, T0
	MOV	p+8(FP), A0		// buffer pointer
	MOVW	n+16(FP), A1		// byte count
write1_loop:
	BEQ	A1, ZERO, write1_done
	MOVBU	(A0), T1
	MOVB	T1, (T0)
	ADD	$1, A0
	ADD	$-1, A1
	JMP	write1_loop
write1_done:
	MOVW	n+16(FP), A0
	MOVW	A0, ret+24(FP)
	RET
write1_suppressed:
	MOVW	n+16(FP), A0
	MOVW	A0, ret+24(FP)
	RET

// read - not supported, return -1
TEXT runtime·read(SB),NOSPLIT|NOFRAME,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// pipe2 - not supported
TEXT runtime·pipe2(SB),NOSPLIT|NOFRAME,$0-20
	MOV	$-1, A0
	MOVW	A0, errno+16(FP)
	RET

// usleep - two-phase implementation:
//
// Phase 1 (early boot): sched_yield — no timer IRQs, so spinning would
//   block forever. Yielding lets other threads run.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real EBREAK with A7=101
//   (SYS_nanosleep). Routes through kmazarin's SyscallNanosleep handler
//   which blocks the thread with a timer deadline and context-switches.
//
// func usleep(usec uint32)
TEXT runtime·usleep(SB),NOSPLIT,$24-4
	// Check if kmazarin syscall handlers are ready
	MOV	runtime·kmazarinSyscallReady(SB), T0
	BNE	T0, ZERO, usleep_real

	// Phase 1: early boot — sched_yield
	MOV	$124, A7		// SYS_sched_yield
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	RET

usleep_real:
	// Phase 2: convert microseconds to timespec and call nanosleep
	MOVW	usec+0(FP), A0		// A0 = microseconds (32-bit, zero-extended)
	MOV	$1000, T0
	MUL	A0, T0, T1		// T1 = nanoseconds (usec * 1000; max ~10ms = 10M ns)
	MOV	ZERO, T2		// tv_sec = 0 (sysmon max is 10ms)
	MOV	T2, 8(X2)		// store tv_sec on stack
	MOV	T1, 16(X2)		// store tv_nsec on stack
	ADD	$8, X2, A0		// A0 = &timespec
	MOV	ZERO, A1		// A1 = NULL (remaining time)
	MOV	$101, A7		// SYS_nanosleep
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	RET

// raise - not implemented
TEXT runtime·raise(SB),NOSPLIT|NOFRAME,$0-4
	RET

// raiseproc - not implemented
TEXT runtime·raiseproc(SB),NOSPLIT|NOFRAME,$0-4
	RET

// setitimer - not implemented
TEXT runtime·setitimer(SB),NOSPLIT|NOFRAME,$0-24
	RET

// timer_create - not implemented
TEXT runtime·timer_create(SB),NOSPLIT,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// timer_settime - not implemented
TEXT runtime·timer_settime(SB),NOSPLIT,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// timer_delete - not implemented
TEXT runtime·timer_delete(SB),NOSPLIT,$0-12
	MOV	$-1, A0
	MOVW	A0, ret+8(FP)
	RET

// mincore - not implemented
TEXT runtime·mincore(SB),NOSPLIT|NOFRAME,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// walltime - return fixed time
TEXT runtime·walltime(SB),NOSPLIT,$0-16
	MOV	$1600000000, A0
	MOV	A0, sec+0(FP)
	MOV	ZERO, A0
	MOVW	A0, nsec+8(FP)
	RET

// nsPerTickX256 — fixed-point (×256) nanoseconds per timer tick.
// Default 25600 = 100 ns/tick × 256 for RISC-V 10 MHz timer.
// Updated by kmazarin's initTimerFrequency() when the real frequency
// is discovered from DTB: nsPerTickX256 = 256000000000 / freq.
DATA	runtime·nsPerTickX256+0(SB)/8, $25600
GLOBL	runtime·nsPerTickX256(SB), NOPTR, $8

// nanotime1 — monotonic nanoseconds from TIME CSR.
// Reads nsPerTickX256 for a dynamic conversion factor:
//   ns = (ticks × nsPerTickX256) >> 8
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	WORD	$0xC01022F3		// csrr a0, time (rdtime)
	MOV	runtime·nsPerTickX256(SB), T0
	MUL	A0, T0, A0		// ticks × nsPerTickX256
	SRL	$8, A0			// >> 8 to remove ×256 scaling
	MOV	A0, ret+0(FP)
	RET

// rtsigprocmask - not implemented
TEXT runtime·rtsigprocmask(SB),NOSPLIT|NOFRAME,$0-28
	RET

// rt_sigaction - register signal handler via EBREAK
// NOTE: On RISC-V, this is dead code — sigaction.go overlay calls
// SyscallRtSigaction directly via linkname, bypassing this stub.
// Kept for reference and for potential use by other architectures
// that share this file's build tag.
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
	MOV	sig+0(FP), A0
	MOV	new+8(FP), A1
	MOV	old+16(FP), A2
	MOV	size+24(FP), A3
	MOV	$134, A7		// SYS_rt_sigaction
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	MOVW	A0, ret+32(FP)
	RET

// sigfwd - not implemented
TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	RET

// sigtramp - signal trampoline, called when a signal is delivered.
// Receives: A0=signum, A1=siginfo_ptr, A2=ucontext_ptr
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$64
	MOVW	A0, 8(X2)		// save signum
	MOV	A1, 16(X2)		// save info
	MOV	A2, 24(X2)		// save ctx
	MOV	$runtime·sigtrampgo(SB), A3
	JALR	RA, A3
	RET

// cgoSigtramp - not implemented
TEXT runtime·cgoSigtramp(SB),NOSPLIT,$0
	RET

// NOTE: mmap and munmap are NOT defined here.
// They are provided by cgo_mmap.go (bump allocator) which is included
// for riscv64 via the build tag. No ECALL needed.

// madvise - no-op, return 0 (advisory only, like ARM64)
TEXT runtime·madvise(SB),NOSPLIT|NOFRAME,$0-28
	MOV	ZERO, A0
	MOVW	A0, ret+24(FP)
	RET

// sched_yield - yield CPU via EBREAK to trap handler
TEXT runtime·sched_yield(SB),NOSPLIT|NOFRAME,$0
	MOV	$124, A7		// SYS_sched_yield
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	RET

// sched_getaffinity - stub
TEXT runtime·sched_getaffinity(SB),NOSPLIT|NOFRAME,$0-32
	MOV	$-1, A0
	MOV	A0, ret+24(FP)
	RET

// epollcreate - not implemented
TEXT runtime·epollcreate(SB),NOSPLIT|NOFRAME,$0-8
	MOV	$-1, A0
	MOVW	A0, ret+4(FP)
	RET

// epollcreate1 - not implemented
TEXT runtime·epollcreate1(SB),NOSPLIT|NOFRAME,$0-8
	MOV	$-1, A0
	MOVW	A0, ret+4(FP)
	RET

// epollctl - not implemented
TEXT runtime·epollctl(SB),NOSPLIT|NOFRAME,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// epollwait - not implemented
TEXT runtime·epollwait(SB),NOSPLIT,$0-40
	MOV	$-1, A0
	MOVW	A0, ret+32(FP)
	RET

// setNonblock - not implemented
TEXT runtime·setNonblock(SB),NOSPLIT|NOFRAME,$0-4
	RET

// clone - create new thread via EBREAK (breakpoint → trap handler)
// EBREAK (scause=3) IS delegated to S-mode via medeleg bit 3.
// The trap handler dispatches via SyscallDispatch using A7=220.
//
// Before the EBREAK, store mp/gp/fn on the child stack at negative
// offsets from stk (A1). SyscallClone reads them from these locations.
// This matches the standard Go runtime convention (sys_linux_riscv64.s)
// and the ARM64/AMD64 kmazarin clone stubs.
TEXT runtime·clone(SB),NOSPLIT,$0-44
	MOV	flags+0(FP), A0
	MOV	stk+8(FP), A1

	// Copy mp, gp, fn off parent stack for use by child.
	MOV	mp+16(FP), T0
	MOV	gp+24(FP), T1
	MOV	fn+32(FP), T2

	// Store on child stack (negative offsets from stack top)
	MOV	T0, -8(A1)		// mp at stk-8
	MOV	T1, -16(A1)		// gp at stk-16
	MOV	T2, -24(A1)		// fn at stk-24
	MOV	$1234, T0
	MOV	T0, -32(A1)		// magic marker for verification

	MOV	$220, A7		// SYS_clone
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch

	// After EBREAK: parent returns with TID > 0, child returns with 0
	BNE	A0, ZERO, clone_parent

	// === Child path ===
	// On new stack. Verify magic marker.
	MOV	-32(X2), T0
	MOV	$1234, T1
	BEQ	T0, T1, clone_good
	// Debug: '!' = magic marker mismatch
	MOV	$0xFFFFFFFF10000000, X28
	MOV	$0x21, X29		// '!'
	MOVB	X29, (X28)
	MOV	ZERO, T0
	MOV	T0, (T0)		// crash: magic marker mismatch

clone_good:
	// Read fn, gp, mp from child stack (negative offsets from SP)
	MOV	-24(X2), T2		// fn
	MOV	-16(X2), T1		// gp (g pointer)
	MOV	-8(X2), T0		// mp (m pointer)

	BEQ	T0, ZERO, clone_nog
	BEQ	T1, ZERO, clone_nog

	// Set up g and m pointers
	// gettid → A0 = tid (for m.procid)
	MOV	$178, A7		// SYS_gettid
	WORD	$0x00100073		// ebreak → trap handler

	MOV	A0, m_procid(T0)	// m.procid = tid

	// Set g_m and g register
	MOV	T0, g_m(T1)		// g.m = mp
	MOV	T1, g			// g register = gp

clone_nog:
	// Save g to TLS
	CALL	runtime·save_g(SB)

	// Call fn (entry function, typically mstart). Never returns.
	JALR	ZERO, T2

	// Should never reach here
	CALL	runtime·abort(SB)

clone_parent:
	MOVW	A0, ret+40(FP)
	RET

// sigaltstack - set/get signal stack via EBREAK
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0-16
	MOV	new+0(FP), A0
	MOV	old+8(FP), A1
	MOV	$132, A7		// SYS_sigaltstack
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	RET

// osyield - NOP yield (WFI can halt CPU when SIE=1 with no timer configured)
TEXT runtime·osyield(SB),NOSPLIT|NOFRAME,$0
	WORD	$0x00000013		// nop (addi x0, x0, 0)
	RET

// sched_getaffinity_trampoline - stub
TEXT runtime·sched_getaffinity_trampoline(SB),NOSPLIT|NOFRAME,$0
	RET

// futex - two-phase implementation:
//
// Phase 1 (early boot, before SetVBAR): spin-check + sched_yield.
//   No kmazarin exception vectors installed, so only basic EBREAKs work.
//   Spin briefly checking for value change, then yield.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real EBREAK with A7=98
//   (SYS_futex). Routes through kmazarin's SyscallFutex handler which
//   properly blocks the thread (FUTEX_WAIT) or wakes blocked threads
//   (FUTEX_WAKE).
//
// func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 unsafe.Pointer, val3 uint32) int32
TEXT runtime·futex(SB),NOSPLIT|NOFRAME,$0
	// Check if kmazarin syscall handlers are ready
	MOV	runtime·kmazarinSyscallReady(SB), T0
	BNE	T0, ZERO, futex_real

	// --- Phase 1: early boot (spin + yield) ---
	MOV	addr+0(FP), A0		// addr
	MOVW	op+8(FP), A1		// op
	MOVW	val+12(FP), A2		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	AND	$0x7F, A1, T0
	MOV	$1, T1
	BEQ	T0, T1, futex_wake_early	// FUTEX_WAKE

	// FUTEX_WAIT: check *addr vs val
	MOVW	(A0), T0		// *addr
	BNE	T0, A2, futex_eagain	// *addr != val → spurious, return EAGAIN

	// *addr == val: spin briefly waiting for change (128 iterations)
	// NOTE: Do NOT use WFI here. Clone children have sstatus.SIE=1 and
	// WFI with SIE=1 but no timer configured halts the CPU on QEMU.
	MOV	$128, T2
futex_spin:
	WORD	$0x00000013		// nop (WFI replacement)
	MOVW	(A0), T0
	BNE	T0, A2, futex_eagain	// Value changed → return EAGAIN
	ADD	$-1, T2
	BNE	T2, ZERO, futex_spin

	// Spin exhausted — yield so other threads can run, then return 0
	// (simulating timeout). Caller re-checks and calls futex again.
	MOV	$124, A7		// SYS_sched_yield
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	MOV	ZERO, A0
	MOVW	A0, ret+40(FP)
	RET

futex_eagain:
	// Value at addr differs from expected val → EAGAIN
	MOV	$-11, A0		// EAGAIN = 11 on Linux
	MOVW	A0, ret+40(FP)
	RET

futex_wake_early:
	MOV	ZERO, A0
	MOVW	A0, ret+40(FP)
	RET

futex_real:
	// --- Phase 2: real futex EBREAK (post-boot) ---
	// Route through kmazarin's SyscallFutex handler for proper blocking/waking.
	MOV	addr+0(FP), A0		// uaddr
	MOVW	op+8(FP), A1		// op
	MOVW	val+12(FP), A2		// val
	MOV	ts+16(FP), A3		// timeout
	MOV	addr2+24(FP), A4	// uaddr2
	MOVW	val3+32(FP), A5		// val3
	MOV	$98, A7			// SYS_futex
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	MOVW	A0, ret+40(FP)
	RET

// getrandom - stub
TEXT runtime·getrandom(SB),NOSPLIT,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// sigreturn - issues rt_sigreturn syscall via EBREAK.
// Called as sa_restorer when the signal handler returns.
// The kernel's SyscallRtSigreturn restores the pre-signal context.
TEXT runtime·sigreturn(SB),NOSPLIT|NOFRAME,$0-0
	MOV	$139, A7		// SYS_rt_sigreturn
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	// Should not return — halt if it does
	WORD	$0x10500073		// WFI
	JMP	-1(PC)

// gettid - get thread ID via EBREAK
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOV	$178, A7		// SYS_gettid
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	MOVW	A0, ret+0(FP)
	RET

// getpid - return 1
TEXT runtime·getpid(SB),NOSPLIT|NOFRAME,$0-8
	MOV	$1, A0
	MOV	A0, ret+0(FP)
	RET

// tgkill - send signal to thread via EBREAK
TEXT runtime·tgkill(SB),NOSPLIT,$0-24
	MOV	tgid+0(FP), A0
	MOV	tid+8(FP), A1
	MOV	sig+16(FP), A2
	MOV	$131, A7		// SYS_tgkill
	WORD	$0x00100073		// ebreak → trap handler → SyscallDispatch
	RET

// kmazarinUART - write a byte to UART via linear map
// func kmazarinUART(c byte)
TEXT runtime·kmazarinUART(SB),NOSPLIT|NOFRAME,$0-1
	MOV	$0xFFFFFFFF10000000, T0
	MOVBU	c+0(FP), T1
	MOVB	T1, (T0)
	RET
