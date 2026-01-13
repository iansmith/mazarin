// abi_stubs_arm64.s - ABI0 entry points for functions called from assembly
//
// When assembly in another package (cardinal/asm/kernel) calls main·functionName,
// Go expects ABI0 entry points. These stubs provide ABI0 wrappers that read
// arguments from the stack and call the ABIInternal implementations with
// arguments in registers.
//
// Pattern from Go runtime (see runtime/asm_arm64.s):
// - ABI0: args on stack, read with name+offset(FP)
// - ABIInternal: args in R0, R1, R2, ...
// - Returns: ABI0 writes to ret+offset(FP), ABIInternal returns in R0, R1, ...

#include "textflag.h"

// timerPreempt is called from async_preempt_arm64.s
// Go signature: func timerPreempt()
// No arguments, no returns - simplest case
TEXT ·timerPreempt(SB), NOSPLIT, $0-0
	// No args to load, just call the implementation
	// The Go compiler will have generated timerPreemptInternal with ABIInternal
	// which takes no args, so we can just call it directly
	BL	·timerPreemptInternal(SB)
	RET

// SyncExceptionDispatch is called from exc_syscall_arm64.s
// Go signature: func SyncExceptionDispatch(
//     ctx *ExceptionContext,
//     syscallNum uint64,
//     arg0, arg1, arg2, arg3, arg4, arg5 uint64,
// ) (result int64, switchTo int32, handled bool)
// ABI0: 8 args (64 bytes) + 3 returns (13 bytes) = 77, round to 80
//
// Tail-call to the internal function. The .abi0 wrapper will read args from
// our caller's stack (which is exactly where they were placed by exc_syscall_arm64.s).
// This avoids adding to the nosplit stack chain.
TEXT ·SyncExceptionDispatch(SB), NOSPLIT, $0-80
	JMP	·syncExceptionDispatchInternal(SB)

// DoContextSwitch is called from exc_syscall_arm64.s
// Go signature: func DoContextSwitch(framePtr uintptr, targetIdx int32) *ThreadContext
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
//
// Tail-call to internal function. The .abi0 wrapper reads from caller's stack.
TEXT ·DoContextSwitch(SB), NOSPLIT, $0-24
	JMP	·doContextSwitchInternal(SB)

// IRQDispatchGo is called from exc_irq_arm64.s
// Go signature: func IRQDispatchGo(
//     irqID uint64, framePtr uintptr, savedG, elr, spEl0 uint64,
// ) (newELR, newSP, newLR uint64, doPreempt bool)
// ABI0: 5 args (40 bytes) + 4 returns (32 bytes) = 72 bytes
//
// Tail-call to internal function. The internal's .abi0 wrapper reads from our stack.
TEXT ·IRQDispatchGo(SB), NOSPLIT, $0-72
	JMP	·IRQDispatchGoInternal(SB)

// KernelMain is called from boot_arm64.s (_cardinal_boot)
// Go signature: func KernelMain(r0, r1, atags uint32)
// ABI0: 3 uint32 args (but on stack as 3*8=24 bytes due to alignment)
// ABIInternal: R0, R1, R2 for args
TEXT ·KernelMain(SB), NOSPLIT, $0-24
	// Load args from stack - uint32 values are at the start of each 8-byte slot
	MOVW	r0+0(FP), R0
	MOVW	r1+8(FP), R1
	MOVW	atags+16(FP), R2

	// Call ABIInternal implementation
	BL	·kernelMainInternal(SB)
	RET
