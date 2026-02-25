// KMAZARIN OVERLAY: Stub out Linux syscalls for bare-metal ARM64 environment
//
// This file replaces runtime/sys_linux_arm64.s
// It provides stub implementations that don't use SVC instructions.
// Kmazarin runs on bare metal with no underlying OS, so all "syscalls"
// are routed through the Go-level dispatcher instead (via ksyscall).

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_arm64.h"

// exit - print diagnostic then infinite loop (no OS to exit to)
TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
	// Write 'E' to UART to signal exit was called
	MOVD	$0xFFFFFFFF09000000, R2
	MOVW	$'E', R3
	MOVW	R3, (R2)
	// Print exit code as 2 hex digits
	MOVW	code+0(FP), R0
	LSR	$4, R0, R1
	AND	$0xF, R1, R1
	CMP	$10, R1
	BLO	exit_dig1
	ADD	$('A'-10), R1, R1
	B	exit_out1
exit_dig1:
	ADD	$'0', R1, R1
exit_out1:
	MOVW	R1, (R2)
	AND	$0xF, R0, R1
	CMP	$10, R1
	BLO	exit_dig2
	ADD	$('A'-10), R1, R1
	B	exit_out2
exit_dig2:
	ADD	$'0', R1, R1
exit_out2:
	MOVW	R1, (R2)
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

// write1 - write bytes to UART via linear map
// On ARM64 QEMU virt, UART PL011 is at PA 0x09000000.
// With KernelVAOffset 0xFFFFFFFF00000000, UART VA = 0xFFFFFFFF09000000.
// When suppressSerial is set (SoftIRQ console active), skip UART output.
TEXT runtime·write1(SB),NOSPLIT|NOFRAME,$0-28
	MOVD	$runtime·suppressSerial(SB), R4
	MOVW	(R4), R5
	CBNZ	R5, write1_suppressed
	MOVD	$0xFFFFFFFF09000000, R2	// UART VA via linear map
	MOVD	p+8(FP), R0		// buffer pointer
	MOVW	n+16(FP), R1		// byte count
write1_loop:
	CBZ	R1, write1_done
	MOVBU	(R0), R3
	MOVW	R3, (R2)
	ADD	$1, R0
	SUB	$1, R1
	B	write1_loop
write1_done:
	MOVW	n+16(FP), R0
	MOVW	R0, ret+24(FP)
	RET
write1_suppressed:
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

// usleep - yield CPU via sched_yield SVC
// Kmazarin has no timer interrupts during early boot, so spinning would
// block forever if another thread (e.g., the parent from clone) is on the
// ready queue. Using sched_yield lets the scheduler run queued threads.
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	MOVD	$124, R8	// SYS_sched_yield
	SVC
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

// walltime - derive from ARM generic timer.
// Returns (seconds, nanoseconds) since an arbitrary epoch.
// Uses CNTVCT_EL0 / 62500000 for seconds, remainder × 16 for nanoseconds.
TEXT runtime·walltime(SB),NOSPLIT,$0-12
	MRS	CNTVCT_EL0, R0
	// Divide by 62500000 (0x3B9ACA0) for seconds — approximate with shift
	// 62.5M ≈ 2^26 (67M), so R0 >> 26 ≈ seconds (close enough for monotonic clock)
	LSR	$26, R0, R1		// R1 ≈ seconds
	MOVD	R1, sec+0(FP)
	// Remainder: R0 - (R1 << 26), then × 16 for nanoseconds
	LSL	$26, R1, R2
	SUB	R2, R0, R0		// R0 = remainder ticks
	LSL	$4, R0, R0		// × 16 ≈ nanoseconds
	MOVW	R0, nsec+8(FP)
	RET

// nanotime1 - read ARM generic timer, convert to approximate nanoseconds.
// CNTVCT_EL0 runs at CNTFRQ_EL0 Hz (62.5 MHz on QEMU virt = 16ns/tick).
// Multiply by 16 (LSL #4) for approximate nanosecond resolution.
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	MRS	CNTVCT_EL0, R0
	LSL	$4, R0, R0	// ×16 ≈ ns for 62.5 MHz counter
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

// futex - implement FUTEX_WAIT with spin-check, FUTEX_WAKE as immediate return.
//
// For FUTEX_WAIT (op & 0x7F == 0): check if *addr == val. If not, return
// EAGAIN (-11). If yes, spin briefly checking for value change, then return 0
// (simulating timeout/spurious wakeup).
//
// For FUTEX_WAKE (op & 0x7F == 1): return 0 immediately.
//
// This prevents infinite spin loops in the Go scheduler by correctly returning
// EAGAIN when the futex value has already changed.
//
// func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 unsafe.Pointer, val3 uint32) int32
TEXT runtime·futex(SB),NOSPLIT|NOFRAME,$0
	MOVD	addr+0(FP), R0		// addr
	MOVW	op+8(FP), R1		// op
	MOVW	val+12(FP), R2		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	AND	$0x7F, R1, R3
	CMP	$1, R3
	BEQ	futex_wake		// FUTEX_WAKE

	// FUTEX_WAIT: check *addr vs val
	MOVW	(R0), R4		// *addr
	CMP	R2, R4
	BNE	futex_eagain		// *addr != val → spurious, return EAGAIN

	// *addr == val: spin briefly waiting for change (128 iterations)
	MOVD	$128, R5
futex_spin:
	YIELD
	MOVW	(R0), R4
	CMP	R2, R4
	BNE	futex_eagain		// Value changed → return EAGAIN
	SUB	$1, R5
	CBNZ	R5, futex_spin

	// Spin exhausted, value didn't change. Yield to scheduler so other
	// threads can run (no timer interrupts during early boot), then return
	// 0 to simulate a timeout/spurious wakeup. The caller (notesleep/lock2)
	// will re-check the condition and call futex again if still waiting.
	MOVD	$124, R8	// SYS_sched_yield
	SVC
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

futex_eagain:
	// Value at addr differs from expected val → EAGAIN
	MOVW	$-11, R0		// EAGAIN = 11 on Linux
	MOVW	R0, ret+40(FP)
	RET

futex_wake:
	// FUTEX_WAKE: return 0 (nothing to wake on bare metal)
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

// clone - issue SVC so kmazarin's exception handler can create a real thread.
// This is a copy of the standard Go runtime clone. It issues SVC #0 with
// SYS_clone (220), which is caught by kmazarin's exception vector table
// (installed at VBAR_EL1 before jump). The SVC handler dispatches to
// kmazarin's SyscallClone, which creates a real ThreadContext for the child.
//
// IMPORTANT: This requires VBAR_EL1 to point to kmazarin's exception vectors.
// Under cardinal, cardinal sets VBAR before jumping.
// Under diplomat, diplomat sets VBAR before jumping.
//
// int64 clone(int32 flags, void *stk, M *mp, G *gp, void (*fn)(void));
TEXT runtime·clone(SB),NOSPLIT|NOFRAME,$0
	MOVW	flags+0(FP), R0
	MOVD	stk+8(FP), R1

	// Copy mp, gp, fn off parent stack for use by child.
	MOVD	mp+16(FP), R10
	MOVD	gp+24(FP), R11
	MOVD	fn+32(FP), R12

	MOVD	R10, -8(R1)
	MOVD	R11, -16(R1)
	MOVD	R12, -24(R1)
	MOVD	$1234, R10
	MOVD	R10, -32(R1)

	MOVD	$220, R8	// SYS_clone
	SVC

	// In parent, return.
	CMP	ZR, R0
	BEQ	clone_child
	MOVW	R0, ret+40(FP)
	RET
clone_child:

	// In child, on new stack.
	MOVD	-32(RSP), R10
	MOVD	$1234, R0
	CMP	R0, R10
	BEQ	clone_good
	MOVD	$0, R0
	MOVD	R0, (R0)	// crash

clone_good:
	// Initialize m->procid to Linux tid
	MOVD	$178, R8	// SYS_gettid
	SVC

	MOVD	-24(RSP), R12     // fn
	MOVD	-16(RSP), R11     // g
	MOVD	-8(RSP), R10      // m

	CMP	$0, R10
	BEQ	clone_nog
	CMP	$0, R11
	BEQ	clone_nog

	MOVD	R0, m_procid(R10)

	// In child, set up new stack
	MOVD	R10, g_m(R11)
	MOVD	R11, g

clone_nog:
	// Call fn
	MOVD	R12, R0
	BL	(R0)

	// It shouldn't return. If it does, exit that thread.
	MOVW	$111, R0
clone_exit:
	MOVD	$93, R8		// SYS_exit
	SVC
	B	clone_exit	// keep exiting

// sigaltstack - no-op
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0
	RET

// osyield - yield with WFE (with diagnostic heartbeat)
TEXT runtime·osyield(SB),NOSPLIT|NOFRAME,$0
	MOVD	$0xFFFFFFFF09000000, R0
	MOVW	$'y', R1
	MOVW	R1, (R0)
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
