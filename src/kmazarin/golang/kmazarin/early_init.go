//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// Assembly functions (defined in runtime_arm64.s)
func readTTBR0() uint64
func dsb()
func isb()
func invalidateTLB()

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

	// Zero L1 entries 0-2 (covers 0x0 - 0xBFFFFFFF, about 3GB)
	// This unmaps Cardinal (0x40100000) and page tables (0x41000000)
	// but those are still accessible via TTBR1 high-memory mappings
	l1Table := (*[512]uint64)(unsafe.Pointer(l1VA))
	for i := 0; i < 3; i++ {
		l1Table[i] = 0
	}

	// Memory barriers and TLB invalidation
	// Sequence: dsb → invalidate → dsb → isb (ensures TLB invalidation completes)
	dsb()
	invalidateTLB()
	dsb()
	isb()
}

// EarlyInit initializes devices that must be set up before DTB scanning
// This includes:
//   - UART (direct mode for early debug output)
//   - GIC (interrupt controller)
//   - Timer (for scheduling/preemption)
//
// Called from init() before the Go runtime is fully initialized
func EarlyInit() {
	Print("[Early] Initializing critical devices...")

	// 1. UART is already working (we're using direct mode)
	Print("[Early]   UART: already initialized (direct mode)")

	// 2. GIC is already initialized by Cardinal - don't reinitialize!
	// Just enable our specific IRQs
	Print("[Early]   GIC: using Cardinal's setup")

	// 3. Enable timer interrupt (IRQ 27) in GIC
	Print("[Early]   Timer IRQ: enabling IRQ 27...")
	EnableTimerIRQ()
	Print("[Early]   Timer IRQ: IRQ 27 enabled")

	// 4. Timer is already armed by Cardinal - will fire soon
	Print("[Early]   Timer: relying on Cardinal's setup")

	// 5. RNG initialization would go here
	// TODO: Add VirtIO RNG initialization when we have VirtIO support
	Print("[Early]   RNG: not yet implemented")

	Print("[Early] Early initialization complete")
}
