package main

import "mazzy/cardinal/asm"

// ThreadContext is referenced from assembly (abi_stubs_arm64.s, exc_syscall_arm64.s).
// Kept as a type definition for linker compatibility.
type ThreadContext struct {
	X    [31]uint64
	SP   uint64
	ELR  uint64
	SPSR uint64
}

// doContextSwitchInternal is the target of the DoContextSwitch ABI stub.
// Called from exc_syscall_arm64.s. Should never be reached now that kmazarin
// installs its own exception vectors.
//
//go:nosplit
func doContextSwitchInternal(framePtr uintptr, targetIdx int32) *ThreadContext {
	uartPutsDirect("BUG: doContextSwitchInternal called\r\n")
	return nil
}

// syncExceptionDispatchInternal handles synchronous exceptions (SVC, data abort).
// Called from exc_syscall_arm64.s via the SyncExceptionDispatch ABI stub.
// Active during cardinal boot; replaced when kmazarin installs its own VBAR_EL1.
//
//go:noinline
func syncExceptionDispatchInternal(
	ctx *ExceptionContext,
	syscallNum uint64,
	arg0, arg1, arg2, arg3, arg4, arg5 uint64,
) (result int64, switchTo int32, handled bool) {
	switch ctx.EC {
	case 0x15: // SVC
		uartPutsDirect("BUG: SVC during cardinal boot\r\n")
		return -38, -1, true
	case 0x25: // Data abort
		faultStatus := ctx.ESR & 0x3F
		if HandlePageFault(uintptr(ctx.FAR), faultStatus) {
			return 0, -1, true
		}
		uartPutsDirect("\r\n!FATAL DATA ABORT: FAR=0x")
		uartPutHex64Direct(ctx.FAR)
		uartPutsDirect(" ELR=0x")
		uartPutHex64Direct(ctx.ELR)
		uartPutsDirect("\r\n")
		return 0, -1, false
	default:
		uartPutsDirect("Unknown exception EC=0x")
		uartPutHex64Direct(ctx.EC)
		uartPutsDirect("\r\n")
		return 0, -1, false
	}
}

// TimerTickHandler is called from the timer IRQ handler in exceptions.go.
// Active during cardinal boot for timer interrupts.
//
//go:nosplit
//go:noinline
func TimerTickHandler() int32 {
	return -1
}

// SyscallExit exits QEMU via semihosting.
//
//go:nosplit
func SyscallExit() {
	asm.SemihostingExit()
	for {
		asm.Nop()
	}
}

