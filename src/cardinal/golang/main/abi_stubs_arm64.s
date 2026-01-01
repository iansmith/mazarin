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
//     ec, esr, elr, far, spsr, syscallNum uint64,
//     arg0, arg1, arg2, arg3, arg4, arg5 uint64,
//     framePtr uintptr,
//     savedFP, savedLR, savedG uint64,
// ) (result int64, switchTo int32, handled bool)
//
// ABI0: 16 uint64 args (128 bytes) + returns (24 bytes) = 152 bytes on stack
// ABIInternal: R0-R15 for args, R0-R2 for returns
TEXT ·SyncExceptionDispatch(SB), NOSPLIT, $0-152
	// Load all 16 arguments from stack into registers
	MOVD	ec+0(FP), R0
	MOVD	esr+8(FP), R1
	MOVD	elr+16(FP), R2
	MOVD	far+24(FP), R3
	MOVD	spsr+32(FP), R4
	MOVD	syscallNum+40(FP), R5
	MOVD	arg0+48(FP), R6
	MOVD	arg1+56(FP), R7
	MOVD	arg2+64(FP), R8
	MOVD	arg3+72(FP), R9
	MOVD	arg4+80(FP), R10
	MOVD	arg5+88(FP), R11
	MOVD	framePtr+96(FP), R12
	MOVD	savedFP+104(FP), R13
	MOVD	savedLR+112(FP), R14
	MOVD	savedG+120(FP), R15

	// Call ABIInternal implementation
	BL	·syncExceptionDispatchInternal(SB)

	// Store return values to stack
	// ABIInternal returns: R0=result, R1=switchTo, R2=handled
	MOVD	R0, result+128(FP)
	MOVW	R1, switchTo+136(FP)
	MOVB	R2, handled+140(FP)
	RET

// DoContextSwitch is called from exc_syscall_arm64.s
// Go signature: func DoContextSwitch(framePtr uintptr, targetIdx int32) *ThreadContext
// ABI0: 2 args (16 bytes) + 1 return (8 bytes) = 24 bytes
TEXT ·DoContextSwitch(SB), NOSPLIT, $0-24
	// Load args from stack
	MOVD	framePtr+0(FP), R0
	MOVW	targetIdx+8(FP), R1

	// Call ABIInternal implementation
	BL	·doContextSwitchInternal(SB)

	// Store return value
	MOVD	R0, ret+16(FP)
	RET

// IRQExceptionDispatch is called from exc_irq_arm64.s
// Go signature: func IRQExceptionDispatch(
//     irqID uint64, framePtr uintptr, savedG, elr, spEl0 uint64,
// ) (newELR, newSP, newLR uint64, doPreempt bool)
// ABI0: 5 args (40 bytes) + 4 returns (32 bytes) = 72 bytes
TEXT ·IRQExceptionDispatch(SB), NOSPLIT, $0-72
	// Load args from stack
	MOVD	irqID+0(FP), R0
	MOVD	framePtr+8(FP), R1
	MOVD	savedG+16(FP), R2
	MOVD	elr+24(FP), R3
	MOVD	spEl0+32(FP), R4

	// Call ABIInternal implementation
	BL	·irqExceptionDispatchInternal(SB)

	// Store return values
	// ABIInternal returns: R0=newELR, R1=newSP, R2=newLR, R3=doPreempt
	MOVD	R0, newELR+40(FP)
	MOVD	R1, newSP+48(FP)
	MOVD	R2, newLR+56(FP)
	MOVB	R3, doPreempt+64(FP)
	RET

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
