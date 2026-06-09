// diplomat/main/hardware.go - Architecture-neutral hardware info types

package main

import "mazzy/shared/bootmem"

// HardwareInfo holds hardware information discovered from UEFI.
type HardwareInfo struct {
	CPUCount uint64 // Number of processors
	RAMBase  uint64 // Physical base address of RAM
	RAMSize  uint64 // Total RAM size in bytes
}

// ramRegionScratch collects usable RAM regions from the UEFI memory map for
// bootmem.LargestContiguousRAM. Package-level (not stack) to keep queryRAM's
// frame small; sized for the most descriptors memMapQueryBuf (32KB) can hold.
var ramRegionScratch [1024]bootmem.Region
