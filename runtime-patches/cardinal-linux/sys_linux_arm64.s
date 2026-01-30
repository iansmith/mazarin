// CARDINAL OVERLAY: Stub out Linux syscalls for bare-metal ARM64 environment
//
// This file replaces runtime/sys_linux_arm64.s
// It provides stub implementations that don't use SVC instructions.
// Cardinal runs on bare metal with no underlying OS, so all "syscalls"
// are routed through the Go-level dispatcher instead.

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_arm64.h"

// exit - infinite loop (no OS to exit to)
TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
halt_loop:
	WFI
	B	halt_loop

// exitThread - infinite loop
TEXT runtime·exitThread(SB),NOSPLIT|NOFRAME,$0-8
	MOVD	wait+0(FP), R0
	MOVW	$0, R1
	STLRW	R1, (R0)
exit_thread_loop:
	WFI
	B	exit_thread_loop

// open - not supported, return -1
TEXT runtime·open(SB),NOSPLIT|NOFRAME,$0-20
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// closefd - no-op, return 0
TEXT runtime·closefd(SB),NOSPLIT|NOFRAME,$0-12
	MOVW	$0, R0
	MOVW	R0, ret+8(FP)
	RET

// write1 - return count (pretend success)
// Actual output goes through UART via the syscall dispatcher
TEXT runtime·write1(SB),NOSPLIT|NOFRAME,$0-28
	MOVW	n+16(FP), R0
	MOVW	R0, ret+24(FP)
	RET

// read - not supported, return -1
TEXT runtime·read(SB),NOSPLIT|NOFRAME,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// pipe2 - not supported
TEXT runtime·pipe2(SB),NOSPLIT|NOFRAME,$0-20
	MOVW	$-1, R0
	MOVW	R0, errno+16(FP)
	RET

// usleep - busy-wait
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	MOVWU	usec+0(FP), R0
usleep_loop:
	SUB	$1, R0, R0
	CBNZ	R0, usleep_loop
	RET

// gettid - return 1
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOVW	$1, R0
	MOVW	R0, ret+0(FP)
	RET

// raise - no-op
TEXT runtime·raise(SB),NOSPLIT|NOFRAME,$0
	RET

// raiseproc - no-op
TEXT runtime·raiseproc(SB),NOSPLIT|NOFRAME,$0
	RET

// getpid - return 1
TEXT ·getpid(SB),NOSPLIT|NOFRAME,$0-8
	MOVD	$1, R0
	MOVD	R0, ret+0(FP)
	RET

// tgkill - no-op
TEXT ·tgkill(SB),NOSPLIT,$0-24
	RET

// setitimer - no-op
TEXT runtime·setitimer(SB),NOSPLIT|NOFRAME,$0-24
	RET

// timer_create - return -1
TEXT runtime·timer_create(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// timer_settime - return -1
TEXT runtime·timer_settime(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// timer_delete - return -1
TEXT runtime·timer_delete(SB),NOSPLIT,$0-12
	MOVW	$-1, R0
	MOVW	R0, ret+8(FP)
	RET

// mincore - return -1
TEXT runtime·mincore(SB),NOSPLIT|NOFRAME,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// walltime - return 0
TEXT runtime·walltime(SB),NOSPLIT,$0-12
	MOVD	$0, R0
	MOVD	R0, sec+0(FP)
	MOVW	$0, R1
	MOVW	R1, nsec+8(FP)
	RET

// nanotime1 - return 0
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	MOVD	$0, R0
	MOVD	R0, ret+0(FP)
	RET

// rtsigprocmask - no-op
TEXT runtime·rtsigprocmask(SB),NOSPLIT|NOFRAME,$0-28
	RET

// rt_sigaction - return 0
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
	MOVW	$0, R0
	MOVW	R0, ret+32(FP)
	RET

// callCgoSigaction - no-op
TEXT runtime·callCgoSigaction(SB),NOSPLIT,$0
	MOVW	$0, R0
	MOVW	R0, ret+24(FP)
	RET

// sigfwd - no-op
TEXT runtime·sigfwd(SB),NOSPLIT,$0-32
	RET

// sigtramp - no-op
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$0
	RET

// sigprofNonGoWrapper - no-op
TEXT runtime·sigprofNonGoWrapper<>(SB),NOSPLIT|NOFRAME,$0
	RET

// cgoSigtramp - no-op
TEXT runtime·cgoSigtramp(SB),NOSPLIT|NOFRAME,$0
	RET

// sysMmap - return error (handled by Go dispatcher)
TEXT runtime·sysMmap(SB),NOSPLIT|NOFRAME,$0
	MOVD	$0, R0
	MOVD	R0, p+32(FP)
	MOVD	$-12, R0	// ENOMEM
	MOVD	R0, err+40(FP)
	RET

// callCgoMmap - not supported
TEXT runtime·callCgoMmap(SB),NOSPLIT,$0
	MOVD	$-1, R0
	MOVD	R0, ret+32(FP)
	RET

// sysMunmap - no-op
TEXT runtime·sysMunmap(SB),NOSPLIT|NOFRAME,$0
	RET

// callCgoMunmap - no-op
TEXT runtime·callCgoMunmap(SB),NOSPLIT,$0
	RET

// madvise - no-op, return 0
TEXT runtime·madvise(SB),NOSPLIT|NOFRAME,$0
	MOVW	$0, R0
	MOVW	R0, ret+24(FP)
	RET

// futex - return 0
TEXT runtime·futex(SB),NOSPLIT|NOFRAME,$0
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

// clone - not supported, return -1
TEXT runtime·clone(SB),NOSPLIT|NOFRAME,$0
	MOVD	$-1, R0
	MOVD	R0, ret+40(FP)
	RET

// sigaltstack - no-op
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0
	RET

// osyield - yield with WFE
TEXT runtime·osyield(SB),NOSPLIT|NOFRAME,$0
	YIELD
	RET

// sched_getaffinity - return 0
TEXT runtime·sched_getaffinity(SB),NOSPLIT|NOFRAME,$0
	MOVW	$0, R0
	MOVW	R0, ret+24(FP)
	RET

// access - return -1
TEXT runtime·access(SB),NOSPLIT,$0-20
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// connect - return -1
TEXT runtime·connect(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// socket - return -1
TEXT runtime·socket(SB),NOSPLIT,$0-20
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// sbrk0 - return 0
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	MOVD	$0, R0
	MOVD	R0, ret+0(FP)
	RET

// vgetrandom1 - return -1
TEXT runtime·vgetrandom1<ABIInternal>(SB),NOSPLIT,$0-48
	MOVD	$-1, R0
	RET
