// asm_decl.go - Go function declarations for assembly-implemented functions
//
// These are forward declarations for functions implemented in abi_stubs_arm64.s.
// Go requires these declarations to call assembly functions from Go code.

package main

// timerPreempt is called from async_preempt_arm64.s
// Implemented in abi_stubs_arm64.s
func timerPreempt()

// DoContextSwitch is called from exc_syscall_arm64.s
// Implemented in abi_stubs_arm64.s
func DoContextSwitch(framePtr uintptr, targetIdx int32) *ThreadContext

// IRQDispatchGo is called from exc_irq_arm64.s
// Implemented in abi_stubs_arm64.s as a tail-call to IRQDispatchGoInternal
func IRQDispatchGo(
	irqID uint64, framePtr uintptr, savedG, elr, spEl0 uint64,
) (newELR, newSP, newLR uint64, doPreempt bool)

// SyncExceptionDispatch is called from exc_syscall_arm64.s
// Implemented in abi_stubs_arm64.s
func SyncExceptionDispatch(
	ec uint64,
	esr uint64,
	elr uint64,
	far uint64,
	spsr uint64,
	syscallNum uint64,
	arg0, arg1, arg2, arg3, arg4, arg5 uint64,
	framePtr uintptr,
	savedFP, savedLR, savedG uint64,
) (result int64, switchTo int32, handled bool)
