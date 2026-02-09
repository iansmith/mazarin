//go:build amd64

// diplomat/main/dtb_amd64.go - Synthesize DTB for QEMU Q35 platform (x86_64)
//
// When UEFI doesn't provide a DTB (ACPI mode, which is default on x86),
// this builds a minimal FDT blob describing the QEMU Q35 hardware layout.
// Kmazarin's DTB parser consumes it to discover block devices and other hardware.

package main

// buildSyntheticDTB allocates a UEFI page and builds a DTB describing the
// QEMU Q35 platform's devices. Returns the physical address of the DTB,
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
	b.PropString("compatible", "qemu,q35")

	// Memory node
	b.BeginNodeAddr("memory", hw.RAMBase)
	b.PropString("device_type", "memory")
	b.PropRegEntry(hw.RAMBase, hw.RAMSize)
	b.EndNode()

	// QEMU Q35 has IDE/SATA drives on the AHCI controller
	// We have two IDE drives: esp-kmazarin.img (boot) and disk-amd64.img (data)
	// The second drive (disk-amd64.img) is what we want for userspace programs
	// On QEMU Q35, SATA drives appear as /dev/sd* in Linux

	// SATA disk 0 (ESP boot disk) - skip this one
	// SATA disk 1 (data disk with dapope/stdio)
	b.BeginNodeAddr("sata-disk", 1)
	b.PropString("compatible", "ata,disk")
	b.PropString("device_type", "block")
	b.PropU32("drive-index", 1) // Second IDE drive
	b.EndNode()

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
