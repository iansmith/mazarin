#include "textflag.h"

// kmazarinYieldImpl — ARM64 stub.
// ARM64 keeps SVC instructions (hardware side effects required for demand paging),
// so this is not called from the runtime overlay on ARM64. Provided only to
// satisfy the linker for the Go declaration in yield_bridge.go.
TEXT ·kmazarinYieldImpl(SB), NOSPLIT|NOFRAME, $0-0
	JMP	·YieldToReadyThread(SB)
