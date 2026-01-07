//go:build qemuvirt && aarch64

#include "textflag.h"

// DoContextSwitch performs a context switch via ABI stub pattern
// Called from assembly exception handlers (SVC, timer IRQ)
// Parameters (stack-based, ABI0):
//   framePtr uintptr - pointer to exception frame
//   targetIdx int32  - index of thread to switch to
// Returns:
//   *ThreadContext - pointer to new thread's context (for assembly to load)
//
TEXT ·DoContextSwitch(SB), NOSPLIT, $0
	JMP ·doContextSwitchInternal(SB)
