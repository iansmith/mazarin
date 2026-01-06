// pte.go - ARM64 Page Table Entry Constants
//
// This file defines bit patterns and masks for ARM64 page table entries.
// These are shared between Cardinal and Kmazarin for page table manipulation.

package constants

// ============================================================================
// Page Table Entry Bits (ARM64)
// ============================================================================

const (
	// Lower attributes (bits 0-11)
	PTE_VALID = 1 << 0 // Valid bit (must be 1)

	// Bit 1 is a "type" bit used differently by level:
	// - For L0-L2: bits[1:0] = 0b11 indicates a table descriptor.
	// - For L3:    bits[1:0] = 0b11 indicates a page descriptor.
	// Leaving bit1 = 0 in an L3 entry yields bits[1:0] = 0b01 which is INVALID at L3
	// and causes a level-3 translation fault (including on instruction fetch).
	PTE_TABLE = 1 << 1
	PTE_PAGE  = 0 // Unused; we always emit L3 pages with bits[1:0] = 0b11.

	// Page attributes (bits 2-7)
	PTE_AF = 1 << 10 // Access flag (must be 1 for hardware-managed)
	PTE_NG = 1 << 11 // Not global (0 = global, 1 = per-ASID)

	// Upper attributes (bits 12-63)
	PTE_UXN  = 1 << 54 // Unprivileged execute never
	PTE_PXN  = 1 << 53 // Privileged execute never
	PTE_CONT = 1 << 52 // Contiguous hint
	PTE_DBM  = 1 << 51 // Dirty bit modifier
	PTE_GP   = 1 << 50 // Guarded page
	PTE_nT   = 1 << 16 // Not translation table walk

	// Software-defined bits (bits 58-55, ignored by MMU hardware)
	// These bits can be used by the OS for page metadata/bookkeeping
	PTE_SW_LOCKED   = 1 << 55 // Page is locked, don't free
	PTE_SW_RESERVED = 1 << 56 // Page reserved for kernel use
	PTE_SW_KERNEL   = 1 << 57 // Kernel-owned page
	PTE_SW_USER     = 1 << 58 // User-accessible page

	// Execute permission flags
	PTE_EXEC_ALLOW = 0                  // PXN=0, UXN=0: Allow execution
	PTE_EXEC_NEVER = PTE_PXN | PTE_UXN  // PXN=1, UXN=1: Never execute

	// Memory attributes (bits 2-4, MAIR index)
	// MAIR[0] = Normal, Inner/Outer Write-Back Cacheable (0xFF)
	// MAIR[1] = Device-nGnRnE (0x00)
	// MAIR[2] = Normal, Inner/Outer Non-Cacheable (0x44)
	PTE_ATTR_NORMAL       = 0 << 2 // MAIR index 0
	PTE_ATTR_DEVICE       = 1 << 2 // MAIR index 1
	PTE_ATTR_NONCACHEABLE = 2 << 2 // MAIR index 2 - for page tables

	// Shareability (bits 8-9)
	PTE_SH_INNER = 3 << 8 // Inner shareable
	PTE_SH_OUTER = 2 << 8 // Outer shareable
	PTE_SH_NONE  = 0 << 8 // Non-shareable

	// Access permissions (bits 6-7)
	PTE_AP_RW     = 0 << 6 // Read/Write at EL0
	PTE_AP_RW_EL1 = 1 << 6 // Read/Write at EL1, no access at EL0
	PTE_AP_RO     = 2 << 6 // Read-only at EL0
	PTE_AP_RO_EL1 = 3 << 6 // Read-only at EL1, no access at EL0

	// Physical address mask for extracting PA from PTE
	// ARMv8-A: Output address is in bits 47:12 of the descriptor
	// Must mask out both lower bits (11:0) and upper attribute bits (63:48)
	PTE_ADDR_MASK = 0x0000FFFFFFFFF000
)

// ============================================================================
// Page Table Size Constants
// ============================================================================

const (
	PAGE_SHIFT = 12                   // log2(PAGE_SIZE)
	PAGE_SIZE  = 1 << PAGE_SHIFT      // 4096 bytes
	PTE_SIZE   = 8                    // 8 bytes per entry
	PTE_COUNT  = 512                  // 512 entries per table
	TABLE_SIZE = PTE_COUNT * PTE_SIZE // 4KB per table

	// Level shifts (address bits used at each level)
	L0_SHIFT = 39 // Bits 48-39
	L1_SHIFT = 30 // Bits 38-30
	L2_SHIFT = 21 // Bits 29-21
	L3_SHIFT = 12 // Bits 20-12
)
