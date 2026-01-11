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

// unmapCardinal unmaps Cardinal's memory region by zeroing only the relevant L0 entry.
// Cardinal is loaded at 0x40100000, which is in L0 entry 0 (VA[47:39] = 0).
// The Go heap is in TTBR0 at 0x4000000000+ (L0 entry 32+), which we must preserve.
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
		Print("[UnmapCardinal] ERROR: RuntimeConfig not available!")
		return
	}

	Print("[UnmapCardinal] Unmapping Cardinal (L0 entry 0 only, preserving heap)...")

	// Read TTBR0_EL1 to get Cardinal's L0 page table physical address
	ttbr0Phys := readTTBR0()
	Print("[UnmapCardinal]   TTBR0_EL1 (L0 PA): 0x")
	printHex(ttbr0Phys)
	Print("")

	// Convert to high-memory virtual address so we can access it
	// The page table region is identity-mapped to high memory by Cardinal
	ttbr0VA := uintptr(ttbr0Phys + cfg.KernelVAOffset)
	Print("[UnmapCardinal]   L0 VA (high memory): 0x")
	printHex(uint64(ttbr0VA))
	Print("")

	// Only zero L0 entry 0, which covers 0x0 - 0x7FFFFFFFFF (512GB)
	// This includes Cardinal at 0x40100000 but NOT the heap at 0x4000000000
	// (heap is in L0 entry 32: 0x4000000000 >> 39 = 32)
	l0Table := (*[512]uint64)(unsafe.Pointer(ttbr0VA))
	oldEntry0 := l0Table[0]
	l0Table[0] = 0

	Print("[UnmapCardinal]   Zeroed L0[0] (was 0x")
	printHex(oldEntry0)
	Print(")")

	// Memory barriers and TLB invalidation
	dsb()  // Ensure all writes complete
	invalidateTLB()  // Invalidate entire TLB (includes DSB+ISB)

	Print("[UnmapCardinal] Cardinal unmapped (0x0-0x7FFFFFFFFF)")
	Print("[UnmapCardinal] Heap preserved (0x4000000000+)")
	Print("")
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
