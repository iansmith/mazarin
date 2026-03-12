package block

import "mazzy/kmazarin/asm"

// bootYieldForIO yields to QEMU's event loop so pending block I/O can complete.
// On RISC-V TCG, reading the VirtIO ISR register via MMIO forces a vCPU exit
// which lets QEMU's main loop process the virtqueue. This avoids depending on
// WFI + interrupt delivery state (SIE/SEIE) which is fragile during early boot
// when the timer may be disabled and interrupt returns may leave SIE cleared.
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
		asm.StiHlt()
	}
}
