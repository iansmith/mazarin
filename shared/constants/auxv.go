// shared/constants/auxv.go - Shared AT_* constants for diplomat and kmazarin
//
// These constants define custom auxv entries passed from the bootloader
// (cardinal or diplomat) to kmazarin via the argc/argv/envp/auxv stack layout.
//
// Standard Linux AT_* values (0-50 range) are defined by the kernel.
// Custom AT_* values use the 0x1000+ range to avoid conflicts.

package constants

const (
	// Standard Linux AT_* values used in our auxv
	AT_NULL   = 0
	AT_PAGESZ = 6
	AT_HWCAP  = 16
	AT_SECURE = 23
	AT_RANDOM = 25

	// Cardinal/diplomat boot information passed via auxv
	AT_DTB_PHYS         = 0x1000 // Physical address of DTB
	AT_DTB_SIZE         = 0x1001 // Size of DTB in bytes
	AT_KMAZARIN_PHYS    = 0x1002 // Physical base address of kmazarin binary
	AT_KMAZARIN_SIZE    = 0x1003 // Total size of kmazarin binary in bytes
	AT_FRAME_POOL_START = 0x1004 // Start of physical frame pool
	AT_FRAME_POOL_END   = 0x1005 // End of physical frame pool
	AT_KERNEL_UART_BASE = 0x1006 // High-memory UART base
	AT_KERNEL_GIC_BASE  = 0x1007 // High-memory GIC base
	AT_TTBR1_L0_PHYS    = 0x1008 // Physical address of TTBR1 L0 page table
	AT_STARTUP_PARAMS   = 0x1009 // High-memory address of StartupParams BSS array
	AT_FRAMEBUFFER_PHYS = 0x100A // Physical address of VirtIO GPU framebuffer
	AT_FRAMEBUFFER_SIZE = 0x100B // Size of framebuffer in bytes

	// New constants for diplomat
	AT_CPU_COUNT          = 0x100C // Number of CPUs
	AT_RAM_BASE           = 0x100D // Physical RAM base address
	AT_RAM_SIZE           = 0x100E // Total RAM size
	AT_UNIFIED_POOL_START = 0x100F // Unified pool start PA
	AT_TTBR0_L0_PHYS     = 0x1013 // TTBR0 L0 physical address

	// Kernel heap bounds - parsed by runtime overlay before first mmap
	AT_KMAZARIN_HEAP_START = 0x1010 // Start of kernel heap VA space
	AT_KMAZARIN_HEAP_END   = 0x1011 // End of kernel heap VA space
	AT_UNIFIED_POOL_END    = 0x1012 // Unified pool end PA
)
