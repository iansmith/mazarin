package block

import "mazzy/kmazarin/asm"

// yieldForIO halts the vCPU until an interrupt fires (HLT on x86_64).
// Used by the interrupt-driven I/O path to wait for block completion.
//
//go:nosplit
func yieldForIO() {
	asm.Hlt()
}
