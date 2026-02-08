// KMAZARIN OVERLAY: Stub out Linux syscalls for bare-metal RISC-V environment
//
// This file replaces runtime/sys_linux_riscv64.s
// It provides stub implementations that don't use ECALL instructions.
// Kmazarin runs on bare metal with no underlying OS, so all "syscalls"
// are routed through the Go-level dispatcher instead (via ksyscall).

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
	MOV	$'W', T1
	MOVB	T1, (T0)
	
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

// usleep - yield CPU via sched_yield SVC
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	// SVC 124 (sched_yield) via ECALL
	MOV	$124, A7
	WORD	$0x00000073	// ecall
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

// rt_sigaction - not implemented
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
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

// mmap - allocate memory via SVC
TEXT runtime·mmap(SB),NOSPLIT,$0-32
	MOV	addr+0(FP), A0
	MOV	n+8(FP), A1
	MOVW	prot+16(FP), A2
	MOVW	flags+20(FP), A3
	MOVW	fd+24(FP), A4
	MOVW	off+28(FP), A5
	MOV	$222, A7	// __NR_mmap
	WORD	$0x00000073	// ecall
	MOV	$-4096, T0
	BGEU	A0, T0, mmap_err
	MOV	A0, p+32(FP)
	RET
mmap_err:
	MOV	ZERO, A0
	MOV	A0, p+32(FP)
	RET

// munmap - free memory via SVC
TEXT runtime·munmap(SB),NOSPLIT,$0-16
	MOV	addr+0(FP), A0
	MOV	n+8(FP), A1
	MOV	$215, A7	// __NR_munmap
	WORD	$0x00000073	// ecall
	BGEU	A0, ZERO, munmap_fail
	RET
munmap_fail:
	WORD	$0x00100073	// ebreak (trap)

// madvise - not implemented
TEXT runtime·madvise(SB),NOSPLIT|NOFRAME,$0-28
	MOV	addr+0(FP), A0
	MOV	n+8(FP), A1
	MOVW	flags+16(FP), A2
	MOV	$233, A7	// __NR_madvise
	WORD	$0x00000073	// ecall
	MOVW	A0, ret+24(FP)
	RET

// sched_yield - yield CPU
TEXT runtime·sched_yield(SB),NOSPLIT|NOFRAME,$0
	MOV	$124, A7	// __NR_sched_yield
	WORD	$0x00000073	// ecall
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

// clone - create new thread via SVC
TEXT runtime·clone(SB),NOSPLIT,$0-44
	MOV	flags+0(FP), A0
	MOV	stk+8(FP), A1
	MOV	mp+16(FP), A2
	MOV	gp+24(FP), A3
	MOV	fn+32(FP), A4
	
	MOV	$220, A7	// __NR_clone
	WORD	$0x00000073	// ecall
	
	BGEU	A0, ZERO, clone_parent
	// Child path - should not reach here in stub
	WORD	$0x00100073	// ebreak
clone_parent:
	MOVW	A0, ret+40(FP)
	RET

// sigaltstack - not implemented
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0-24
	RET

// osyield - yield via sched_yield
TEXT runtime·osyield(SB),NOSPLIT|NOFRAME,$0
	MOV	$124, A7	// __NR_sched_yield
	WORD	$0x00000073	// ecall
	RET

// sched_getaffinity - stub (duplicate - should be same as above)
TEXT runtime·sched_getaffinity_trampoline(SB),NOSPLIT|NOFRAME,$0
	RET

// futex - futex syscall
TEXT runtime·futex(SB),NOSPLIT,$0-32
	MOV	addr+0(FP), A0
	MOVW	op+8(FP), A1
	MOVW	val+12(FP), A2
	MOV	ts+16(FP), A3
	MOV	addr2+24(FP), A4
	MOVW	val3+32(FP), A5
	MOV	$98, A7		// __NR_futex
	WORD	$0x00000073	// ecall
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
