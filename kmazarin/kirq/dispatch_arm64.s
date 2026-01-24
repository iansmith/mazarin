//go:build !test_stubs


#include "textflag.h"

// DispatchNonTimerIRQ is the ABI0 entry point called from external assembly.
// Uses tail-call to the internal Go implementation to handle ABI conversion.
//
// The external assembly (exceptions_arm64.s) calls this via ABI0.
// The JMP (tail-call) to dispatchNonTimerIRQInternal lets Go's generated
// .abi0 wrapper handle the conversion to ABIInternal.
//
// This function takes no parameters (IRQ number is passed via global).
TEXT ·DispatchNonTimerIRQ(SB), NOSPLIT, $0
	JMP	·dispatchNonTimerIRQInternal(SB)
