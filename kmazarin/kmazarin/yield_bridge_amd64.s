#include "textflag.h"

// kmazarinYieldImpl — direct yield without INT $0x80.
// Tail-calls YieldToReadyThread which saves context, finds a ready thread,
// and either IRETs to it or returns if none is available.
// NOSPLIT|NOFRAME: no stack frame — JMP preserves caller's return address on stack.
TEXT ·kmazarinYieldImpl(SB), NOSPLIT|NOFRAME, $0-0
	JMP	·YieldToReadyThread(SB)
