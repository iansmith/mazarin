// addresses.go - High-Memory Kernel Address Constants
//
// This file contains FIXED constants for high-memory (TTBR1) kernel addressing.
// These values are hardcoded design decisions and QEMU hardware addresses.
//
// IMPORTANT: Link-time computed values (like actual binary sizes and section
// addresses) are NOT in this file - they are patched by compute-linker-values.go
// into linker symbols accessible via asm_decl.go functions.

package constants

// ============================================================================
// Kernel Virtual Base Address
// ============================================================================
// This is a DESIGN DECISION - the base address where the kernel is mapped
// in high memory (TTBR1 space). This must have bit 63 set (high half).

const (
	// Kernel virtual base - mirrors physical RAM offset in high memory
	// Physical 0x4000_0000 -> Virtual 0xFFFF_FFFF_4000_0000
	KernelVABase = 0xFFFFFFFF40000000
)

// ============================================================================
// MMIO Device Virtual Addresses
// ============================================================================
// QEMU virt machine has fixed MMIO device addresses. We map them to high
// memory with a fixed offset.

const (
	// MMIO mapping offset - add to physical address to get kernel VA
	KernelMMIOOffset = 0xFFFFFFFF00000000

	// UART PL011 - physical 0x0900_0000 -> virtual 0xFFFF_FFFF_0900_0000
	KernelUartBase = KernelMMIOOffset + 0x09000000

	// GIC - physical 0x0800_0000 -> virtual 0xFFFF_FFFF_0800_0000
	KernelGicBase = KernelMMIOOffset + 0x08000000
)

// ============================================================================
// Kernel Memory Limits (Policy Decisions)
// ============================================================================

const (
	// Maximum size for kmazarin binary (text + rodata + data + bss)
	// This is enforced at boot - if the actual binary is larger, boot will fail
	KmazarinBinaryMaxSize = 0x00800000 // 8MB

	// Total memory limit for kmazarin (binary + heap combined)
	KmazarinTotalLimit = 512 * 1024 * 1024 // 512MB

	// Stack sizes - tuned for tail-call ABI stub pattern
	// These are MUCH smaller than original because tail-calls don't add stack frames
	KernelG0StackSize  = 0x4000 // 16KB (was 64KB) - Go runtime init needs ~8KB peak
	KernelExcStackSize = 0x2000 // 8KB (was 128KB) - exception handling is shallow

	// Page table allocation for TTBR1
	KernelPageTableSize = 0x00800000 // 8MB (policy)

	// Parameter buffer size
	ParamBufferSize = 128 * 1024 // 128KB
)

// ============================================================================
// High-Memory Stack Addresses
// ============================================================================
// These are VIRTUAL addresses in kernel space (TTBR1)

const (
	// g0 stack - used for normal kernel execution in EL1t mode (SP_EL0)
	KernelG0StackTop    = 0xFFFFFFFF5F000000
	KernelG0StackBottom = KernelG0StackTop - KernelG0StackSize

	// Exception stack - used for exception handlers in EL1h mode (SP_EL1)
	KernelExcStackTop    = 0xFFFFFFFF5F008000
	KernelExcStackBottom = KernelExcStackTop - KernelExcStackSize
)

// ============================================================================
// Notes on Link-Time Computed Values
// ============================================================================
//
// The following values are NOT constants - they are computed by parsing
// the kmazarin ELF file and patched into the binary by compute-linker-values.go:
//
// - Kmazarin text start/end addresses
// - Kmazarin rodata start/end addresses
// - Kmazarin data start address
// - Kmazarin BSS end address
// - Kmazarin total static size (text+rodata+data+bss)
//
// These are accessed via functions in asm_decl.go:
//   GetKmazarinTextStart() uintptr
//   GetKmazarinTextEnd() uintptr
//   GetKmazarinRodataStart() uintptr
//   GetKmazarinRodataEnd() uintptr
//   GetKmazarinDataStart() uintptr
//   GetKmazarinBssEnd() uintptr
//   GetKmazarinTotalStatic() uintptr
