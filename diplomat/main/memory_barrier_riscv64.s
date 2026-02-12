// Memory barrier for RISC-V VirtIO operations
// This ensures memory ordering for device I/O

#include "textflag.h"

// memoryBarrier performs a full memory fence (rw,rw)
// FENCE ensures all previous memory operations complete before any subsequent ones
TEXT ·memoryBarrier(SB), NOSPLIT, $0-0
	FENCE
	RET
