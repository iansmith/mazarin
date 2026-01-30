// runtime_config.go - Shared runtime configuration structure
//
// This structure is populated by Cardinal and consumed by Kmazarin.
// It contains ONLY values that kmazarin cannot derive on its own:
//   - DtbPhysAddr: only the bootloader knows where QEMU placed the DTB
//   - KmazarinSize: cardinal loaded the ELF and computed the size

package constants

// RuntimeConfig holds the minimal configuration that cardinal must provide.
// All other memory layout values are derived by kmazarin from constants,
// CPU registers, and DTB parsing.
//
// CRITICAL: This struct is shared between Cardinal and Kmazarin. Any changes
// must be coordinated between both codebases to maintain compatibility.
type RuntimeConfig struct {
	// Physical address of DTB (only bootloader knows where QEMU placed it)
	DtbPhysAddr uint64

	// Size of kmazarin binary (cardinal loaded the ELF and computed this)
	KmazarinSize uint64

	// Physical frame pool for kmazarin kernel heap.
	// These depend on cardinal's physical frame allocator state after
	// loading kmazarin, mapping page tables, etc. Kmazarin cannot derive
	// these because the physical frame layout depends on cardinal's
	// allocation order.
	FramePoolStart uint64
	FramePoolEnd   uint64
}
