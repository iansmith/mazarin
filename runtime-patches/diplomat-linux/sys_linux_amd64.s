// diplomat/runtime-patches/diplomat-linux/sys_linux_amd64.s
// UEFI-compatible runtime functions for x86_64
//
// This file replaces syscall-based functions with UEFI-compatible implementations
// or stubs for the Diplomat bootloader environment.

#include "go_asm.h"
#include "go_tls.h"
#include "textflag.h"
#include "cgo/abi_amd64.h"

// runtime·settls sets the FS register for Thread Local Storage
// In UEFI, we use WRFSBASE instruction to set FS directly
// This requires CPU FSGSBASE support (available on modern CPUs)
TEXT runtime·settls(SB),NOSPLIT,$0
	MOVQ	DI, SI
	ADDQ	$8, SI	// ELF wants to use -8(FS)

	// Use WRFSBASE to set FS register directly
	// WRFSBASE is available on CPUs with FSGSBASE feature (Intel Ivy Bridge+, AMD Excavator+)
	// Instruction encoding: 0xF3 0x48 0x0F 0xAE 0xD6 for wrfsbase %rsi
	// This is the safest way to set FS in UEFI without syscalls
	BYTE $0xF3; BYTE $0x48; BYTE $0x0F; BYTE $0xAE; BYTE $0xD6 // wrfsbase %rsi

	// If WRFSBASE is not supported, we'll get an invalid opcode exception
	// But most modern CPUs support it, and QEMU definitely does
	RET

// runtime·osyield yields the processor (no-op in UEFI)
TEXT runtime·osyield(SB),NOSPLIT,$0
	RET

// runtime·exit exits the program
TEXT runtime·exit(SB),NOSPLIT,$0-4
	// In UEFI, we can't really exit - just hang
hang:
	JMP hang

// runtime·exitThread exits a thread
TEXT runtime·exitThread(SB),NOSPLIT,$0-8
	MOVQ	wait+0(FP), AX
	MOVL	$0, (AX)
hang:
	JMP hang

// runtime·open opens a file
// Parameters: path+0(FP), flags+8(FP), mode+12(FP)
// Returns: ret+16(FP) - int32
TEXT runtime·open(SB),NOSPLIT,$0-20
	MOVQ	path+0(FP), DI
	MOVL	flags+8(FP), SI
	MOVL	mode+12(FP), DX
	CALL	main·DiplomatOpen(SB)
	MOVL	AX, ret+16(FP)
	RET

// runtime·closefd closes a file descriptor
// Parameters: fd+0(FP)
// Returns: ret+8(FP) - int32
TEXT runtime·closefd(SB),NOSPLIT,$0-12
	MOVL	fd+0(FP), DI
	CALL	main·DiplomatClose(SB)
	MOVL	AX, ret+8(FP)
	RET

// runtime·write1 writes to a file descriptor
// Parameters: fd+0(FP), buf+8(FP), count+16(FP)
// Returns: ret+24(FP) - int32 (bytes written or -errno)
TEXT runtime·write1(SB),NOSPLIT,$0-28
	MOVL	fd+0(FP), DI
	MOVQ	buf+8(FP), SI
	MOVL	count+16(FP), DX
	CALL	main·DiplomatWrite(SB)
	MOVL	AX, ret+24(FP)
	RET

// runtime·read reads from a file descriptor
// Parameters: fd+0(FP), buf+8(FP), count+16(FP)
// Returns: ret+24(FP) - int32
TEXT runtime·read(SB),NOSPLIT,$0-28
	MOVL	fd+0(FP), DI
	MOVQ	buf+8(FP), SI
	MOVL	count+16(FP), DX
	CALL	main·DiplomatRead(SB)
	MOVL	AX, ret+24(FP)
	RET

// runtime·usleep sleeps for a number of microseconds (stub)
TEXT runtime·usleep(SB),NOSPLIT,$0-4
	RET

// runtime·usleep_no_g is like usleep but can be called from g0 (stub)
TEXT runtime·usleep_no_g(SB),NOSPLIT,$0-4
	RET

// runtime·gettid gets the thread ID (stub - return 1)
TEXT runtime·gettid(SB),NOSPLIT,$0-8
	MOVQ	$1, AX
	MOVQ	AX, ret+0(FP)
	RET

// runtime·raise raises a signal (stub)
TEXT runtime·raise(SB),NOSPLIT,$0-4
	RET

// runtime·raiseproc raises a signal on the process (stub)
TEXT runtime·raiseproc(SB),NOSPLIT,$0-4
	RET

// runtime·getpid gets the process ID (stub - return 1)
TEXT runtime·getpid(SB),NOSPLIT,$0-8
	MOVQ	$1, AX
	MOVQ	AX, ret+0(FP)
	RET

// runtime·sigaltstack sets the signal stack (stub)
TEXT runtime·sigaltstack(SB),NOSPLIT,$0-16
	RET

// runtime·setitimer sets an interval timer (stub)
TEXT runtime·setitimer(SB),NOSPLIT,$0-24
	RET

// runtime·mincore checks if pages are in core (stub)
TEXT runtime·mincore(SB),NOSPLIT,$0-28
	MOVL	$-1, AX
	MOVL	AX, ret+24(FP)
	RET

// runtime·walltime1 gets the wall clock time (stub - return epoch)
TEXT runtime·walltime1(SB),NOSPLIT,$0-16
	MOVQ	$0, sec+0(FP)
	MOVL	$0, nsec+8(FP)
	RET

// runtime·nanotime1 gets monotonic time (stub - return 0)
TEXT runtime·nanotime1(SB),NOSPLIT,$0-8
	MOVQ	$0, ret+0(FP)
	RET

// runtime·rtsigprocmask sets the signal mask (stub)
TEXT runtime·rtsigprocmask(SB),NOSPLIT,$0-28
	RET

// runtime·sigaction sets a signal handler (stub)
TEXT runtime·rt_sigaction(SB),NOSPLIT,$0-36
	RET

// runtime·mmap maps memory
// Parameters (ABI0 stack layout):
//   addr+0(FP)    - uintptr  - requested address (0=any, hint, or MAP_FIXED)
//   length+8(FP)  - uint64   - size in bytes
//   prot+16(FP)   - int32    - protection flags
//   flags+20(FP)  - int32    - mapping flags (MAP_FIXED, etc.)
//   fd+24(FP)     - int32    - file descriptor (ignored)
//   offset+28(FP) - int64    - file offset (ignored)
//   ret+24(FP)    - uintptr  - return value (address or -errno)
//
// Calls main.DiplomatMmap (Go function) with all parameters
TEXT runtime·mmap(SB),NOSPLIT,$0-32
	// Load all parameters
	MOVQ	addr+0(FP), DI      // arg0: addr
	MOVQ	length+8(FP), SI    // arg1: length
	MOVL	prot+16(FP), DX     // arg2: prot
	MOVL	flags+20(FP), CX    // arg3: flags
	MOVL	fd+24(FP), R8       // arg4: fd
	MOVQ	offset+28(FP), R9   // arg5: offset (int64 but passed as arg6)

	// Call main.DiplomatMmap(addr, length, prot, flags, fd, offset) int64
	CALL	main·DiplomatMmap(SB)

	// Result is in AX (int64)
	MOVQ	AX, ret+24(FP)
	RET

// runtime·munmap unmaps memory
// Parameters: addr+0(FP), length+8(FP)
// Returns: ret+16(FP) - int32 (0=success, -errno)
TEXT runtime·munmap(SB),NOSPLIT,$0-16
	MOVQ	addr+0(FP), DI
	MOVQ	length+8(FP), SI
	CALL	main·DiplomatMunmap(SB)
	MOVL	AX, ret+16(FP)
	RET

// runtime·madvise advises on memory usage
// Parameters: addr+0(FP), length+8(FP), advice+16(FP)
// Returns: ret+24(FP) - int32
TEXT runtime·madvise(SB),NOSPLIT,$0-28
	MOVQ	addr+0(FP), DI
	MOVQ	length+8(FP), SI
	MOVL	advice+16(FP), DX
	CALL	main·DiplomatMadvise(SB)
	MOVL	AX, ret+24(FP)
	RET

// runtime·futex performs a futex operation
// Parameters (ABI0 stack layout):
//   uaddr+0(FP)    - *uint32  - futex word address
//   op+8(FP)       - int32    - futex operation
//   val+12(FP)     - uint32   - operation value
//   timeout+16(FP) - *timespec - timeout (ignored)
//   uaddr2+24(FP)  - *uint32  - second address (for REQUEUE)
//   val3+32(FP)    - uint32   - operation value
//   ret+40(FP)     - int32    - return value
//
// Calls main.DiplomatFutex(uaddr, op, val, timeout, uaddr2, val3) int64
TEXT runtime·futex(SB),NOSPLIT,$0-48
	MOVQ	uaddr+0(FP), DI     // arg0: uaddr
	MOVL	op+8(FP), SI        // arg1: op (int32 promoted to int64 in register)
	MOVL	val+12(FP), DX      // arg2: val (uint32 promoted to uint64)
	MOVQ	timeout+16(FP), CX  // arg3: timeout
	MOVQ	uaddr2+24(FP), R8   // arg4: uaddr2
	MOVL	val3+32(FP), R9     // arg5: val3 (uint32 promoted to uint64)

	// Call main.DiplomatFutex
	CALL	main·DiplomatFutex(SB)

	// Result is in AX (int64)
	// Store as int32 in return slot
	MOVL	AX, ret+40(FP)
	RET

// runtime·clone creates a new thread (stub - not implemented yet)
TEXT runtime·clone(SB),NOSPLIT,$0-32
	MOVL	$-1, AX
	MOVL	AX, ret+28(FP)
	RET

// runtime·sigreturn returns from a signal (stub)
TEXT runtime·sigreturn(SB),NOSPLIT,$0-0
	RET

// runtime·sched_getaffinity gets CPU affinity (stub)
TEXT runtime·sched_getaffinity(SB),NOSPLIT,$0-24
	MOVL	$-1, AX
	MOVL	AX, ret+20(FP)
	RET
