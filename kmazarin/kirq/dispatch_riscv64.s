#include "textflag.h"

// DispatchNonTimerIRQ is the ABI0 entry point called from trap handler assembly.
// Tail-call to the internal Go implementation for ABI conversion.
TEXT ·DispatchNonTimerIRQ(SB), NOSPLIT, $0
	JMP	·dispatchNonTimerIRQInternal(SB)
