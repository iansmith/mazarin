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

// exit - terminate entire process (all threads of this shepherd)
// Go runtime calls this for os.Exit() and runtime.throw() — it means
// "kill the whole process", matching Linux exit_group(2) semantics.
TEXT runtime·exit(SB),NOSPLIT|NOFRAME,$0-4
	MOVW	code+0(FP), R0		// arg0: exit status
	MOVD	$94, R8			// SYS_exit_group (terminate all threads)
	SVC	$0
	// Should not return — kernel terminates shepherd and switches away
halt_loop:
	WFI
	B	halt_loop

// exitThread - signal thread exit to runtime, then call kernel exit syscall
TEXT runtime·exitThread(SB),NOSPLIT|NOFRAME,$0-8
	MOVD	wait+0(FP), R0
	MOVW	$0, R1
	STLRW	R1, (R0)		// Store-release 0 to *wait (signal to runtime)
	MOVD	$0, R0			// exit status 0
	MOVD	$93, R8			// SYS_exit
	SVC	$0
	// Should not return
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
// When suppressSerial is set (SoftIRQ console active), skip UART output —
// UNLESS the runtime is dying (panicking != 0): a throw's println/traceback
// must reach the serial port even while the SoftIRQ console owns the UART.
// Without this bypass, kernel throws die silently as a bare
// "KERNEL EXIT GROUP" with the reason lost (MAZ-136 netpoll hunt).
TEXT runtime·write1(SB),NOSPLIT|NOFRAME,$0-28
	MOVD	$runtime·suppressSerial(SB), R4
	MOVW	(R4), R5
	CBZ	R5, write1_go
	MOVD	$runtime·panicking(SB), R4	// fatal throw/panic in progress?
	MOVW	(R4), R5
	CBZ	R5, write1_suppressed
write1_go:
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

// usleep - two-phase implementation:
//
// Phase 1 (early boot): sched_yield — no timer IRQs, so spinning would
//   block forever. Yielding lets other threads run.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real SVC 101 (SYS_nanosleep).
//   Routes through kmazarin's SyscallNanosleep handler which blocks the
//   thread with a timer deadline and context-switches to another thread.
//   The thread wakes when the deadline expires. This matches Linux behavior
//   and makes sysmon sleep for the correct duration instead of busy-polling.
//
// func usleep(usec uint32)
TEXT runtime·usleep(SB),NOSPLIT,$24-4
	// Check if kmazarin syscall handlers are ready
	MOVD	runtime·kmazarinSyscallReady(SB), R7
	CBNZ	R7, usleep_real

	// Phase 1: early boot — sched_yield
	MOVD	$124, R8	// SYS_sched_yield
	SVC
	RET

usleep_real:
	// Phase 2: sleep 10 seconds (ignore requested duration).
	// The kernel's 250Hz tick handler wakes us early if GC needs STW.
	// This eliminates ~50K SVCs/sec from sysmon's 20us-10ms polling loop
	// while keeping GC STW latency bounded to 4ms (one tick period).
	MOVD	$10, R6			// tv_sec = 10
	MOVD	R6, 8(RSP)		// store tv_sec on stack
	MOVD	$0, R5			// tv_nsec = 0
	MOVD	R5, 16(RSP)		// store tv_nsec on stack
	ADD	$8, RSP, R0		// R0 = &timespec
	MOVD	$0, R1			// R1 = NULL (remaining time)
	MOVD	$101, R8		// SYS_nanosleep
	SVC
	RET

// gettid — route through kmazarin syscall dispatcher
TEXT runtime·gettid(SB),NOSPLIT,$0-4
	MOVD	$178, R8	// SYS_gettid
	SVC
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

// tgkill — route through kmazarin syscall dispatcher
TEXT ·tgkill(SB),NOSPLIT,$0-24
	MOVD	tgid+0(FP), R0
	MOVD	tid+8(FP), R1
	MOVD	sig+16(FP), R2
	MOVD	$131, R8	// SYS_tgkill
	SVC
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

// nsPerTickX256 — fixed-point (×256) nanoseconds per timer tick.
// Default 4096 = 16 ns/tick × 256 for ARM64 62.5 MHz timer.
// Updated by kmazarin's initTimerFrequency() when the real frequency
// is discovered from CNTFRQ_EL0: nsPerTickX256 = 256000000000 / freq.
DATA	runtime·nsPerTickX256+0(SB)/8, $4096
GLOBL	runtime·nsPerTickX256(SB), NOPTR, $8

// nanotime1 — monotonic nanoseconds from CNTVCT_EL0.
// Reads nsPerTickX256 for a dynamic conversion factor:
//   ns = (ticks × nsPerTickX256) >> 8
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	MRS	CNTVCT_EL0, R0
	MOVD	runtime·nsPerTickX256(SB), R1
	MUL	R1, R0, R0		// ticks × nsPerTickX256
	LSR	$8, R0, R0		// >> 8 to remove ×256 scaling
	MOVD	R0, ret+0(FP)
	RET

// rtsigprocmask - no-op
TEXT runtime·rtsigprocmask(SB),NOSPLIT|NOFRAME,$0-28
	RET

// rt_sigaction — route through kmazarin syscall dispatcher
TEXT runtime·rt_sigaction(SB),NOSPLIT|NOFRAME,$0-36
	MOVW	sig+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	MOVD	size+24(FP), R3
	MOVD	$134, R8	// SYS_rt_sigaction
	SVC
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

// sigtramp — signal trampoline, copied from Go runtime.
// Called when kmazarin delivers a signal: R0=sig, R1=info, R2=ctx.
// Saves callee-saved registers, calls sigtrampgo, restores, returns.
TEXT runtime·sigtramp(SB),NOSPLIT|TOPFRAME,$176
	// Save callee-save registers (in case signal forwarding modifies them)
	SAVE_R19_TO_R28(8*4)
	SAVE_F8_TO_F15(8*14)

	// In kmazarin, g is always set (no CGO), so skip load_g.
	// R0, R1, R2 already contain sig, info, ctx.
	MOVD	$runtime·sigtrampgo<ABIInternal>(SB), R3
	BL	(R3)

	// Restore callee-save registers
	RESTORE_R19_TO_R28(8*4)
	RESTORE_F8_TO_F15(8*14)

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

// madvise — route through kmazarin syscall dispatcher.
// The scavenger calls this to release unused heap pages back to the OS
// (MADV_DONTNEED).  Real reclaim + re-fault-on-next-access zeroes pages,
// which is critical for correctness when heap pages are recycled as stacks.
TEXT runtime·madvise(SB),NOSPLIT|NOFRAME,$0
	MOVD	addr+0(FP), R0		// addr
	MOVD	n+8(FP), R1		// length
	MOVW	advice+16(FP), R2	// advice (MADV_DONTNEED=4, MADV_FREE=8)
	MOVD	$233, R8		// SYS_madvise on ARM64
	SVC
	MOVW	R0, ret+24(FP)
	RET

// futex - two-phase implementation:
//
// Phase 1 (early boot, before SetVBAR): spin-check + sched_yield.
//   No kmazarin exception vectors installed, so only basic SVCs work.
//   Spin briefly checking for value change, then yield.
//
// Phase 2 (post-boot, kmazarinSyscallReady=1): real SVC 98 (SYS_futex).
//   Routes through kmazarin's SyscallFutex handler which properly blocks
//   the thread (FUTEX_WAIT) or wakes blocked threads (FUTEX_WAKE).
//   This matches Linux behavior: threads sleep until woken, with optional
//   timeout via deadline queue.
//
// func futex(addr unsafe.Pointer, op int32, val uint32, ts, addr2 unsafe.Pointer, val3 uint32) int32
TEXT runtime·futex(SB),NOSPLIT|NOFRAME,$0
	// Check if kmazarin syscall handlers are ready
	MOVD	runtime·kmazarinSyscallReady(SB), R7
	CBNZ	R7, futex_real

	// --- Phase 1: early boot (spin + yield) ---
	MOVD	addr+0(FP), R0		// addr
	MOVW	op+8(FP), R1		// op
	MOVW	val+12(FP), R2		// val

	// Check operation type (low 7 bits, ignoring FUTEX_PRIVATE_FLAG)
	AND	$0x7F, R1, R3
	CMP	$1, R3
	BEQ	futex_wake_early	// FUTEX_WAKE

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

	// Spin exhausted — yield so other threads can run, then return 0
	// (simulating timeout). Caller re-checks and calls futex again.
	MOVD	$124, R8	// SYS_sched_yield
	SVC
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

futex_eagain:
	MOVW	$-11, R0		// EAGAIN
	MOVW	R0, ret+40(FP)
	RET

futex_wake_early:
	MOVW	$0, R0
	MOVW	R0, ret+40(FP)
	RET

futex_real:
	// --- Phase 2: real futex SVC (post-boot) ---
	// Route through kmazarin's SyscallFutex handler for proper blocking/waking.
	MOVD	addr+0(FP), R0		// uaddr
	MOVW	op+8(FP), R1		// op
	MOVW	val+12(FP), R2		// val
	MOVD	ts+16(FP), R3		// timeout
	MOVD	addr2+24(FP), R4	// uaddr2
	MOVW	val3+32(FP), R5		// val3
	MOVD	$98, R8			// SYS_futex
	SVC
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

// sigaltstack — route through kmazarin syscall dispatcher
TEXT runtime·sigaltstack(SB),NOSPLIT|NOFRAME,$0-16
	MOVD	new+0(FP), R0
	MOVD	old+8(FP), R1
	MOVD	$132, R8	// SYS_sigaltstack
	SVC
	RET

// osyield - yield
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
