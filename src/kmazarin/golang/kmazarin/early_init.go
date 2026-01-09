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

// unmapCardinal completely unmaps all Cardinal (TTBR0) pages by zeroing the L0 page table.
// After this, all low-memory (0x00000000-0x3FFFFFFF) accesses will fault.
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

	Print("[UnmapCardinal] Cutting off Cardinal by unmapping TTBR0...")

	// Read TTBR0_EL1 to get Cardinal's L0 page table physical address
	ttbr0Phys := readTTBR0()
	Print("[UnmapCardinal]   TTBR0_EL1 (Cardinal L0 PA): 0x")
	printHex(ttbr0Phys)
	Print("")

	// Convert to high-memory virtual address so we can access it
	// The page table region is identity-mapped to high memory by Cardinal
	ttbr0VA := uintptr(ttbr0Phys + cfg.KernelVAOffset)
	Print("[UnmapCardinal]   TTBR0 L0 VA (high memory): 0x")
	printHex(uint64(ttbr0VA))
	Print("")

	// Zero all 512 L0 entries (512 * 8 bytes = 4096 bytes)
	// This unmaps the entire low-memory address space (0x0 - 0x3FFFFFFF)
	l0Table := (*[512]uint64)(unsafe.Pointer(ttbr0VA))
	for i := 0; i < 512; i++ {
		l0Table[i] = 0
	}

	Print("[UnmapCardinal]   Zeroed 512 L0 entries")

	// Memory barriers and TLB invalidation
	dsb()  // Ensure all writes complete
	invalidateTLB()  // Invalidate entire TLB (includes DSB+ISB)

	Print("[UnmapCardinal] Cardinal fully unmapped - all low memory inaccessible")
	Print("[UnmapCardinal] Kmazarin is now standalone (high memory only)")
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
