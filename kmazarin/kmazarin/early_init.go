//go:build !test_stubs

package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"unsafe"
)

// unmapCardinal unmaps Cardinal's memory region at the L1 level.
//
// Memory layout in L0[0] (covers 0 - 512GB with L1 entries of 1GB each):
//   L1[0]:   0x00000000 - 0x3FFFFFFF (unused)
//   L1[1]:   0x40000000 - 0x7FFFFFFF (Cardinal + page tables)
//   L1[2-255]: mostly unused
//   L1[256]: 0x4000000000 - 0x403FFFFFFF (Go heap start)
//
// We zero L1[0-2] to unmap Cardinal while preserving L1[256+] for the heap.
// Page tables at 0x41000000 are still accessible via TTBR1 (high memory).
//
// SAFETY: This MUST be called only after:
//   - We're executing from Kmazarin (high memory)
//   - All RuntimeConfig data has been copied
//   - Early devices are initialized
//   - We no longer need any Cardinal code or data
//
//go:nosplit
func unmapCardinal() {
	cfg := GetRuntimeConfig()
	if cfg == nil {
		KernelPanic("unmapCardinal: RuntimeConfig not available")
	}

	// Read TTBR0_EL1 to get L0 page table physical address
	ttbr0Phys := readTTBR0()

	// Convert to high-memory virtual address (page tables mapped via TTBR1)
	l0VA := uintptr(ttbr0Phys + cfg.KernelVAOffset)

	// Read L0[0] to get L1 table address
	l0Table := (*[512]uint64)(unsafe.Pointer(l0VA))
	l0Entry0 := l0Table[0]

	// Check if L0[0] is valid and points to a table
	if (l0Entry0 & 0x3) != 0x3 {
		KernelPanic("unmapCardinal: L0[0] is not a valid table descriptor")
	}

	// Extract L1 table physical address (bits 47:12)
	l1Phys := l0Entry0 & 0x0000FFFFFFFFF000
	l1VA := uintptr(l1Phys + cfg.KernelVAOffset)

	// Zero L1 entries 1-2 (covers 0x40000000 - 0xBFFFFFFF)
	// This unmaps Cardinal (0x40100000) and page tables (0x41000000)
	// but those are still accessible via TTBR1 high-memory mappings
	//
	// IMPORTANT: We keep L1[0] (0x0 - 0x3FFFFFFF) mapped because it contains
	// device MMIO regions like UART (0x09000000), GIC (0x08000000), etc.
	l1Table := (*[512]uint64)(unsafe.Pointer(l1VA))
	for i := 1; i < 3; i++ { // Start from 1, not 0!
		l1Table[i] = 0
	}

	// Memory barriers and TLB invalidation
	// Sequence: dsb → invalidate → dsb → isb (ensures TLB invalidation completes)
	dsb()
	invalidateTLB()
	dsb()
	isb()
}

// EarlyInit initializes the early console and thread management.
// Called from init() before the Go runtime is fully initialized.
//
// Boot sequence:
// 1. Set up MMIOUartConsole using Cardinal's UART base (allows debug output)
// 2. Initialize thread management
// 3. Later, main() will run device discovery and wire up interrupts
func EarlyInit() {
	// Set up early console using direct MMIO writes
	// This allows debug output before device discovery and interrupt setup
	uartBase := GetUartBase()
	earlyConsole := console.NewMMIOUartConsole(uartBase)
	console.Set(earlyConsole)

	// Initialize thread management system
	InitThreads()

	// Register all device drivers (no hardware access yet)
	device.RegisterAllDrivers()
}
