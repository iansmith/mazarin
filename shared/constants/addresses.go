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

	// Kernel memory budget: fixed allocation for kernel region
	KernelMemoryBudget = 64 * 1024 * 1024 // 64MB

	// Minimum RAM required
	MinRAMRequired = 256 * 1024 * 1024 // 256MB

	// Stack sizes - MUST match cardinal/golang/constants/layout.go
	// These were doubled from 16KB/8KB to debug potential stack overflow issues
	KernelG0StackSize  = 0x8000 // 32KB - g0 stack for normal kernel execution
	KernelExcStackSize = 0x4000 // 16KB - exception stack for handlers

	// Page table allocation for TTBR1
	KernelPageTableSize = 0x00800000 // 8MB (policy)

	// Parameter buffer size
	ParamBufferSize = 128 * 1024 // 128KB
)

// ============================================================================
// High-Memory Stack Addresses
// ============================================================================
// These are VIRTUAL addresses in kernel space (TTBR1)
// COMPUTED from base address - no hardcoded addresses!
// Memory layout: g0 stack immediately followed by exception stack

const (
	// Base address for kernel stacks in high memory
	// Placed in the gap between PT identity map end (0x43800000) and
	// linear map start (~0x44200000) to avoid creating gaps in the linear map.
	KernelStacksVirtBase = 0xFFFFFFFF43E00000

	// g0 stack - used for normal kernel execution in EL1t mode (SP_EL0)
	// Range: 0xFFFFFFFF43E00000 - 0xFFFFFFFF43E08000 (32KB)
	KernelG0StackBottom = KernelStacksVirtBase
	KernelG0StackTop    = KernelG0StackBottom + KernelG0StackSize

	// Exception stack - used for exception handlers in EL1h mode (SP_EL1)
	// Range: 0xFFFFFFFF43E08000 - 0xFFFFFFFF43E0C000 (16KB)
	KernelExcStackBottom = KernelG0StackTop
	KernelExcStackTop    = KernelExcStackBottom + KernelExcStackSize
)

// ============================================================================
// Kernel Text and PT Pool Layout
// ============================================================================
// These are used by kmazarin to compute its own memory layout.

const (
	// KernelTextBase is the high-memory VA where kmazarin code starts.
	// Computed from KernelMMIOOffset + KmazarinLoadAddr.
	// KmazarinLoadAddr is defined in shared/constants/layout.go.
	KernelTextBase = KernelMMIOOffset + KmazarinLoadAddr // 0xFFFFFFFF42000000

	// TTBR1 page table region and PT pool sizes (fixed policy decisions)
	KernelTTBR1RegionSize = 0x10000 // 64KB (16 page tables max)
	KernelPTPoolSize      = 0x80000 // 512KB (128 pages)
)

// ============================================================================
// Kernel Heap Virtual Address Space
// ============================================================================
// Demand-paged heap for kmazarin. Go runtime reserves large contiguous VA
// regions for arena metadata. VA space is free - only accessed pages consume
// physical frames.
//
// Heap addresses are arch-specific:
// - ARM64: 0xFFFF000100000000 (TTBR1 space, valid VA)
// - x86_64: 0xFFFF800100000000 (canonical negative half, required for 48-bit paging)
//
// See addresses_arm64.go and addresses_amd64.go for arch-specific values.
// KernelHeapSize is computed from them.

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
