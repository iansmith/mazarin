// DIPLOMAT OVERLAY: Stub out Linux syscalls for UEFI environment (ARM64)
//
// This file replaces runtime/sys_linux_arm64.s
// It provides stub implementations that don't use the SVC instruction

#include "textflag.h"
#include "go_asm.h"

// exit - halt with WFI instruction
TEXT runtime·exit(SB),NOSPLIT,$0-4
	// In UEFI, we can't really exit - just halt the CPU
halt_loop:
	WFI
	B	halt_loop

// exitThread - halt (same as exit in single-threaded UEFI)
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	WFI
	B	-1(PC)

// open - not supported, return -1
TEXT runtime·open(SB),NOSPLIT,$0-20
	MOVD	$-1, R0
	MOVD	R0, ret+16(FP)
	RET

// closefd - no-op, return 0
TEXT runtime·closefd(SB),NOSPLIT,$0-12
	MOVW	$0, R0
	MOVW	R0, ret+8(FP)
	RET

// write1 - CRITICAL: this must not use syscall
// We need to actually write for panic messages to be visible
// For now, just return the count as if it succeeded
TEXT runtime·write1(SB),NOSPLIT,$0-28
	// fd at 0(FP), p at 8(FP), n at 16(FP), ret at 20(FP)
	// Just return n (pretend we wrote everything)
	MOVW	n+16(FP), R0
	MOVW	R0, ret+20(FP)
	RET

// read - not supported, return -1
TEXT runtime·read(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// pipe2 - not supported
TEXT runtime·pipe2(SB),NOSPLIT,$0-20
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// usleep - busy-wait approximation
TEXT runtime·usleep(SB),NOSPLIT,$16
	// Simple busy loop - not accurate but works
	MOVW	usec+0(FP), R0
usleep_loop:
	SUBW	$1, R0, R0
	CBNZ	R0, usleep_loop
	RET

// gettid - return 1 (main thread)
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOVW	$1, R0
	MOVW	R0, ret+0(FP)
	RET

// raise - no-op in UEFI
TEXT runtime·raise(SB),NOSPLIT,$0
	RET

// raiseproc - no-op in UEFI
TEXT runtime·raiseproc(SB),NOSPLIT,$0
	RET

// setitimer - not supported
TEXT runtime·setitimer(SB),NOSPLIT,$0-24
	RET

// timer_create - not supported, return -1
TEXT runtime·timer_create(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// timer_settime - not supported, return -1
TEXT runtime·timer_settime(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// timer_delete - not supported, return -1
TEXT runtime·timer_delete(SB),NOSPLIT,$0-12
	MOVW	$-1, R0
	MOVW	R0, ret+8(FP)
	RET

// mincore - not supported, return -1
TEXT runtime·mincore(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// nanotime1 - return 0 for now (no real time support)
// TODO: Could use UEFI GetTime or CNTVCT_EL0
TEXT runtime·nanotime1(SB),NOSPLIT,$16-8
	MOVD	$0, R0
	MOVD	R0, ret+0(FP)
	RET

// rtsigprocmask - no-op
TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	RET

// rt_sigaction - no-op, return 0
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	MOVW	$0, R0
	MOVW	R0, ret+32(FP)
	RET

// callCgoSigaction - no-op
TEXT runtime·callCgoSigaction(SB),NOSPLIT,$16
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

// sysMmap - implemented in mmap.go via linkname
// This stub is here in case it's called before our Go override is set up
TEXT runtime·sysMmap(SB),NOSPLIT,$0
	// Return error - should be handled by Go override
	MOVD	$-1, R0
	MOVD	R0, ret+32(FP)
	RET

// callCgoMmap - no-op
TEXT runtime·callCgoMmap(SB),NOSPLIT,$16
	MOVD	$-1, R0
	MOVD	R0, ret+0(FP)
	RET

// sysMunmap - no-op for now
TEXT runtime·sysMunmap(SB),NOSPLIT,$0
	RET

// callCgoMunmap - no-op
TEXT runtime·callCgoMunmap(SB),NOSPLIT,$16-16
	RET

// madvise - no-op
TEXT runtime·madvise(SB),NOSPLIT,$0
	RET

// futex - implemented in futex.go via linkname
// This stub returns 0 (success)
TEXT runtime·futex(SB),NOSPLIT,$0
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

// clone - not supported in UEFI (single-threaded for now)
TEXT runtime·clone(SB),NOSPLIT|NOFRAME,$0
	// Return error
	MOVD	$-1, R0
	MOVD	R0, ret+64(FP)
	RET

// sigaltstack - no-op
TEXT runtime·sigaltstack(SB),NOSPLIT,$0
	RET

// settls - set TPIDR_EL0 for TLS
// This is called by Go runtime, but we set up TLS ourselves in entry_arm64.s
TEXT runtime·settls(SB),NOSPLIT,$0
	// base is at 0(FP)
	// Set TPIDR_EL0 for TLS
	MOVD	base+0(FP), R0
	MSR	R0, TPIDR_EL0
	RET

// osyield - no-op (single-threaded)
TEXT runtime·osyield(SB),NOSPLIT,$0
	YIELD
	RET

// sched_getaffinity - return 0 (no affinity support)
TEXT runtime·sched_getaffinity(SB),NOSPLIT,$0
	MOVW	$0, R0
	MOVW	R0, ret+24(FP)
	RET

// access - not supported
TEXT runtime·access(SB),NOSPLIT,$0
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// connect - not supported
TEXT runtime·connect(SB),NOSPLIT,$0-28
	MOVW	$-1, R0
	MOVW	R0, ret+24(FP)
	RET

// socket - not supported
TEXT runtime·socket(SB),NOSPLIT,$0-20
	MOVW	$-1, R0
	MOVW	R0, ret+16(FP)
	RET

// sbrk0 - return 0
TEXT runtime·sbrk0(SB),NOSPLIT,$0-8
	MOVD	$0, R0
	MOVD	R0, ret+0(FP)
	RET

// getpid - return 1 (fake PID)
TEXT runtime·getpid(SB),NOSPLIT,$0-8
	MOVD	$1, R0
	MOVD	R0, ret+0(FP)
	RET

// tgkill - no-op (can't kill threads in UEFI)
TEXT runtime·tgkill(SB),NOSPLIT,$0-20
	MOVW	$0, R0
	MOVW	R0, ret+16(FP)
	RET

// vgetrandom1 - return error (no random source in UEFI stub)
TEXT runtime·vgetrandom1(SB),NOSPLIT,$0
	// Return -1 to indicate failure
	MOVD	$-1, R0
	MOVD	R0, ret+24(FP)
	RET

// walltime - return 0 (no wall clock in UEFI stub)
TEXT runtime·walltime(SB),NOSPLIT,$0-16
	MOVD	$0, R0
	MOVD	R0, sec+0(FP)
	MOVW	$0, R0
	MOVW	R0, nsec+8(FP)
	RET
