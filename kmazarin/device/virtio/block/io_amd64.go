package block

import "mazzy/kmazarin/asm"

// bootYieldForIO halts the vCPU until the next interrupt or event.
// Used only by doBlockIO's polling loop during early boot (TOML config read,
// ELF loading) before the scheduler and disk priest are running. Once the disk
// priest is active, block I/O goes through blockReadInterrupt which does a
// proper scheduler transition to thread 0's idle loop.
//
//go:nosplit
func bootYieldForIO() {
	asm.Hlt()
}
