package block

import "mazzy/kmazarin/asm"

// bootYieldForIO yields to QEMU's event loop so pending block I/O can complete.
// On x86_64 TCG, reading the VirtIO ISR register via MMIO forces a vCPU exit
// which lets QEMU's main loop process the virtqueue. This avoids depending on
// STI+HLT + MSI-X interrupt delivery, which hangs when the timer is disabled
// (no periodic interrupt to wake from HLT) and MSI-X routing isn't waking the CPU.
//
// Used only by doBlockIO's polling loop during early boot (TOML config read,
// ELF loading) before the scheduler and disk shepherd are running. Once the disk
// shepherd is active, block I/O goes through blockReadInterrupt which does a
// proper scheduler transition to thread 0's idle loop.
//
//go:nosplit
func bootYieldForIO() {
	base := virtioBlockDevice.ISRBase
	if base != 0 {
		_ = asm.MmioRead8(base)
	} else {
		asm.StiHlt()
	}
}
