//go:build qemuvirt && aarch64

package main

// MMIO device addresses (QEMU virt machine)
// These are mapped to high memory by Cardinal before jumping to Kmazarin.
// Physical addresses are in the 0x08000000-0x10000000 range,
// mapped to high memory (0xFFFFFFFF00000000 + physical).
//
// These addresses are specific to the QEMU virt machine and will need
// updating if we move to different hardware.
const (
	// High-memory UART base (PL011)
	// Physical: 0x09000000
	// Virtual:  0xFFFFFFFF09000000
	UartBase = 0xFFFFFFFF09000000

	// High-memory GIC base
	// Physical: 0x08000000
	// Virtual:  0xFFFFFFFF08000000
	GicDistBase = 0xFFFFFFFF08000000 // GIC Distributor
	GicCpuBase  = 0xFFFFFFFF08010000 // GIC CPU Interface

	// High-memory address mask
	// Used to validate that addresses are in high memory (TTBR1)
	HighMemoryBase = 0xFFFFFFFF00000000
)

// IsHighMemory checks if an address is in the high-memory region (TTBR1).
// All kmazarin code, data, and MMIO must be in high memory.
//
//go:nosplit
func IsHighMemory(addr uintptr) bool {
	return addr >= HighMemoryBase
}

// IsLowMemory checks if an address is in the low-memory region (TTBR0).
// After Cardinal unmapping, any low-memory access will fault.
//
//go:nosplit
func IsLowMemory(addr uintptr) bool {
	return addr < HighMemoryBase
}
