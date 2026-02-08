//go:build riscv64

// diplomat/main/dtb_riscv64.go - Synthesize DTB for QEMU virt platform (RISC-V)
//
// When UEFI doesn't provide a DTB (rare on RISC-V UEFI, but possible),
// this builds a valid FDT blob from known QEMU virt hardware layout.
// Kmazarin's DTB parser consumes it identically to a firmware-provided DTB.

package main

// buildSyntheticDTB allocates a UEFI page and builds a DTB describing the
// QEMU virt platform's devices (RISC-V specific). Returns the physical address
// of the DTB, or 0 on failure.
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
	b.PropString("compatible", "riscv-virtio")
	b.PropString("model", "riscv-virtio,qemu")

	// CPUs node
	b.BeginNode("cpus")
	b.PropU32("#address-cells", 1)
	b.PropU32("#size-cells", 0)
	b.PropU32("timebase-frequency", 10000000) // 10 MHz (typical for QEMU)

	// CPU nodes (one per hardware CPU)
	for i := uint64(0); i < hw.CPUCount; i++ {
		b.BeginNodeAddr("cpu", i)
		b.PropString("device_type", "cpu")
		b.PropU32("reg", uint32(i))
		b.PropString("compatible", "riscv")
		b.PropString("mmu-type", "riscv,sv39")
		b.PropString("riscv,isa", "rv64imafdcsu")
		b.PropString("status", "okay")
		b.EndNode()
	}
	b.EndNode() // cpus

	// Memory node
	b.BeginNodeAddr("memory", hw.RAMBase)
	b.PropString("device_type", "memory")
	b.PropRegEntry(hw.RAMBase, hw.RAMSize)
	b.EndNode()

	// PLIC (Platform-Level Interrupt Controller)
	// QEMU virt RISC-V: PLIC @ 0xc000000
	b.BeginNodeAddr("plic", 0xc000000)
	b.PropString("compatible", "riscv,plic0")
	b.PropRegEntry(0xc000000, 0x4000000) // 64MB region
	b.PropU32("#interrupt-cells", 1)
	b.PropEmpty("interrupt-controller")
	b.PropU32("riscv,ndev", 96) // 96 external interrupts
	b.EndNode()

	// CLINT (Core-Local Interruptor)
	// QEMU virt RISC-V: CLINT @ 0x2000000
	b.BeginNodeAddr("clint", 0x2000000)
	b.PropString("compatible", "riscv,clint0")
	b.PropRegEntry(0x2000000, 0x10000) // 64KB
	b.EndNode()

	// NS16550 UART
	// QEMU virt RISC-V: UART0 @ 0x10000000, IRQ 10
	b.BeginNodeAddr("uart", 0x10000000)
	b.PropString("compatible", "ns16550a")
	b.PropRegEntry(0x10000000, 0x100)
	b.PropU32("clock-frequency", 3686400) // 3.6864 MHz
	b.PropU32("interrupts", 10)
	b.EndNode()

	// VirtIO MMIO devices
	// QEMU virt RISC-V: 8 slots starting at 0x10001000, stride 0x1000
	// Interrupts 1-8
	for n := uint32(0); n < 8; n++ {
		addr := uint64(0x10001000) + uint64(n)*0x1000
		b.BeginNodeAddr("virtio_mmio", addr)
		b.PropString("compatible", "virtio,mmio")
		b.PropRegEntry(addr, 0x1000)
		b.PropU32("interrupts", n+1)
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
