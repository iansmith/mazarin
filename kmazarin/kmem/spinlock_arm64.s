// spinlock_arm64.s - ARM64 assembly for spinlock support

#include "textflag.h"

// yieldProcessor executes WFE (Wait For Event) instruction.
// This puts the core in a low-power state until an event occurs.
// On ARM64, store-exclusive instructions (used by atomic.CompareAndSwap)
// automatically generate events, so this works well for spinlocks.
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	WFE
	RET
