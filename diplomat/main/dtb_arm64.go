//go:build arm64

// diplomat/main/dtb_arm64.go - Synthesize DTB for QEMU virt platform
//
// When UEFI doesn't provide a DTB (ACPI mode, which is QEMU's default),
// this builds a valid FDT blob from known QEMU virt hardware layout.
// Kmazarin's DTB parser consumes it identically to a firmware-provided DTB.

package main

// buildSyntheticDTB allocates a UEFI page and builds a DTB describing the
// QEMU virt platform's devices. Returns the physical address of the DTB,
// or 0 on failure.
func buildSyntheticDTB(hw *HardwareInfo) uint64 {
	// Allocate one 4KB UEFI page for the DTB (below 4GB for linear map)
	page := linearMapMaxPA - 1
	status := UEFIAllocatePages(AllocateMaxAddress, EfiLoaderData, 1, &page)
	if status != EFI_SUCCESS {
		printString("Failed to allocate page for synthetic DTB\r\n")
		return 0
	}

	var b FDTBuilder
	b.Init(uintptr(page), 4096)

	// Root node
	b.BeginNode("")
	b.PropU32("#address-cells", 2)
	b.PropU32("#size-cells", 2)
	b.PropString("compatible", "linux,dummy-virt")

	// Memory node
	b.BeginNodeAddr("memory", hw.RAMBase)
	b.PropString("device_type", "memory")
	b.PropRegEntry(hw.RAMBase, hw.RAMSize)
	b.EndNode()

	// GIC (interrupt controller)
	// QEMU virt: GICD @ 0x8000000, GICC @ 0x8010000
	b.BeginNodeAddr("intc", 0x8000000)
	b.PropString("compatible", "arm,cortex-a15-gic")
	b.PropRegMulti([][2]uint64{
		{0x8000000, 0x10000},  // GICD
		{0x8010000, 0x10000},  // GICC
	})
	b.PropU32("#interrupt-cells", 3)
	b.PropEmpty("interrupt-controller")
	b.EndNode()

	// PL011 UART
	// QEMU virt: UART0 @ 0x9000000, SPI 1 (IRQ 33)
	b.BeginNodeAddr("pl011", 0x9000000)
	b.PropString("compatible", "arm,pl011")
	b.PropRegEntry(0x9000000, 0x1000)
	b.PropInterrupt(0, 1, 4) // SPI 1, level-triggered
	b.EndNode()

	// VirtIO MMIO devices
	// QEMU virt: 32 slots starting at 0xa000000, stride 0x200
	// SPI 16+N (IRQ 48+N), edge-triggered
	for n := uint32(0); n < 32; n++ {
		addr := uint64(0xa000000) + uint64(n)*0x200
		b.BeginNodeAddr("virtio_mmio", addr)
		b.PropString("compatible", "virtio,mmio")
		b.PropRegEntry(addr, 0x200)
		b.PropInterrupt(0, 16+n, 1) // SPI 16+N, edge-triggered
		b.EndNode()
	}

	// Close root node
	b.EndNode()

	totalSize := b.Finalize()

	printString("Synthetic DTB at PA ")
	printHex(page)
	printString(" (")
	printHex(uint64(totalSize))
	printString(" bytes)\r\n")

	return page
}
