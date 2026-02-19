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

// exit - print diagnostic then infinite loop (no OS to exit to)
TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
	// Write 'E' to UART to signal exit was called
	// RISC-V QEMU virt: UART NS16550 at PA 0x10000000
	// With KernelVAOffset 0xFFFFFFFF00000000, UART VA = 0xFFFFFFFF10000000
	MOV	$0xFFFFFFFF10000000, T0
	MOV	$'E', T1
	MOVB	T1, (T0)

	// Print exit code as 2 hex digits
	MOVW	code+0(FP), A0
	SRL	$4, A0, T1
	AND	$0xF, T1
	MOV	$10, T2
	BGE	T1, T2, exit_dig1_alpha
	ADD	$'0', T1
	JMP	exit_out1
exit_dig1_alpha:
	ADD	$('A'-10), T1
exit_out1:
	MOVB	T1, (T0)

	AND	$0xF, A0, T1
	BGE	T1, T2, exit_dig2_alpha
	ADD	$'0', T1
	JMP	exit_out2
exit_dig2_alpha:
	ADD	$('A'-10), T1
exit_out2:
	MOVB	T1, (T0)
halt_loop:
	WORD	$0x10500073	// wfi
	JMP	halt_loop

// exitThread - infinite loop
TEXT runtime·exitThread(SB),NOSPLIT|NOFRAME,$0-8
	MOV	wait+0(FP), A0
	MOV	ZERO, T0
	WORD	$0x1805352F	// amoswap.w.rl zero, zero, (a0)
exit_thread_loop:
	WORD	$0x10500073	// wfi
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
TEXT runtime·write1(SB),NOSPLIT|NOFRAME,$0-28
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

// usleep - yield CPU via EBREAK to trap handler → scheduler
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	MOV	$124, A7		// SYS_sched_yield
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

// nanotime1 - return monotonic time from TIME CSR
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	WORD	$0xC01022F3	// csrr a0, time (rdtime)
	MOV	$10000000, T0	// 10MHz timebase
	MUL	A0, T0, A0	// Convert ticks to nanoseconds (approximate)
	MOV	A0, ret+0(FP)
	RET

// rtsigprocmask - not implemented
TEXT runtime·rtsigprocmask(SB),NOSPLIT|NOFRAME,$0-28
	RET

// rt_sigaction - return 0 (success), signals are not supported
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
	MOV	ZERO, A0
	MOVW	A0, ret+32(FP)
	RET

// sigfwd - not implemented
TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	RET

// sigtramp - not implemented
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$176
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

// sigaltstack - not implemented
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0-24
	RET

// osyield - NOP yield (WFI can halt CPU when SIE=1 with no timer configured)
TEXT runtime·osyield(SB),NOSPLIT|NOFRAME,$0
	WORD	$0x00000013		// nop (addi x0, x0, 0)
	RET

// sched_getaffinity_trampoline - stub
TEXT runtime·sched_getaffinity_trampoline(SB),NOSPLIT|NOFRAME,$0
	RET

// futex - spin-wait implementation (no ECALL, mirrors ARM64 pattern)
TEXT runtime·futex(SB),NOSPLIT|NOFRAME,$0
	MOV	addr+0(FP), A0		// addr
	MOVW	op+8(FP), A1		// op
	MOVW	val+12(FP), A2		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	AND	$0x7F, A1, T0
	MOV	$1, T1
	BEQ	T0, T1, futex_wake	// FUTEX_WAKE

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

	// Spin exhausted, value didn't change. Yield to scheduler so other
	// threads can run, then return 0 to simulate a timeout/spurious wakeup.
	// The caller (notesleep/lock2) will re-check and call futex again.
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

futex_wake:
	// FUTEX_WAKE: return 0 (nothing to wake on bare metal)
	MOV	ZERO, A0
	MOVW	A0, ret+40(FP)
	RET

// getrandom - stub
TEXT runtime·getrandom(SB),NOSPLIT,$0-28
	MOV	$-1, A0
	MOVW	A0, ret+24(FP)
	RET

// sigreturn - not implemented
TEXT runtime·sigreturn(SB),NOSPLIT|NOFRAME,$0-0
	RET

// gettid - return 1
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOV	$1, A0
	MOVW	A0, ret+0(FP)
	RET

// getpid - return 1
TEXT runtime·getpid(SB),NOSPLIT|NOFRAME,$0-8
	MOV	$1, A0
	MOV	A0, ret+0(FP)
	RET

// tgkill - no-op
TEXT runtime·tgkill(SB),NOSPLIT,$0-24
	RET

// kmazarinUART - write a byte to UART via linear map
// func kmazarinUART(c byte)
TEXT runtime·kmazarinUART(SB),NOSPLIT|NOFRAME,$0-1
	MOV	$0xFFFFFFFF10000000, T0
	MOVBU	c+0(FP), T1
	MOVB	T1, (T0)
	RET
