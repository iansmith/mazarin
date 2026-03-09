package block

import "mazzy/kmazarin/asm"

// bootYieldForIO causes a VM exit so QEMU's event loop can process pending
// block I/O. On ARM64 under HVF, WFI can deadlock when a stale GIC interrupt
// prevents proper WFI trapping. Reading the VirtIO ISR register via MMIO
// forces a VM exit without depending on interrupt state.
//
// Used only by doBlockIO's polling loop during early boot (TOML config read,
// ELF loading) before the scheduler and disk priest are running. Once the disk
// priest is active, block I/O goes through blockReadInterrupt which does a
// proper scheduler transition to thread 0's idle loop.
//
//go:nosplit
func bootYieldForIO() {
	base := virtioBlockDevice.ISRBase
	if base != 0 {
		_ = asm.MmioRead8(base)
	} else {
		asm.Wfi()
	}
}
