package block

import "mazzy/kmazarin/asm"

// yieldForIO reads the VirtIO MMIO InterruptStatus register (offset 0x60).
// Each MMIO read causes a VM exit under QEMU, giving the event loop
// time to process the pending block I/O request.
// Falls back to WFI for non-MMIO devices.
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
