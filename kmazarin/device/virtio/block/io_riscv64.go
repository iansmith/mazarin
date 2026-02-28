package block

import "mazzy/kmazarin/asm"

// yieldForIO reads the VirtIO MMIO InterruptStatus register (offset 0x60).
// Each MMIO read causes a VM exit under QEMU TCG, giving the event loop
// time to process the pending block I/O request. WFI alone is insufficient
// because QEMU TCG may treat WFI as a NOP when no interrupt is pending.
//
//go:nosplit
func yieldForIO() {
	base := virtioBlockDevice.MMIOBase
	if base != 0 {
		_ = asm.MmioRead32(base + 0x60)
	} else {
		asm.Wfi()
	}
}
