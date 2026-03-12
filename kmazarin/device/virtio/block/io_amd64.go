package block

import "mazzy/kmazarin/asm"

// bootYieldForIO atomically enables interrupts and halts the vCPU until the
// next interrupt fires. Uses STI+HLT (the STI shadow makes this atomic) so
// the CPU wakes regardless of the caller's IF state. Bare HLT is wrong here
// because IF can be cleared by timer IRQ exception return paths that don't
// force IF=1 for kernel-mode returns.
//
// Used only by doBlockIO's polling loop during early boot (TOML config read,
// ELF loading) before the scheduler and disk priest are running. Once the disk
// priest is active, block I/O goes through blockReadInterrupt which does a
// proper scheduler transition to thread 0's idle loop.
//
//go:nosplit
func bootYieldForIO() {
	asm.StiHlt()
}
