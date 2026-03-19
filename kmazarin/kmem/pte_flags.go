// pte_flags.go - Architecture-neutral page table entry flag representation.
//
// PTEFlags provides a portable way for kmazarin code to inspect and construct
// page table entries without knowing the target architecture. Each platform's
// paging_*.go translates between PTEFlags and native PTE format.
//
// IMPORTANT: No Go interfaces. Many callers are //go:nosplit exception handlers
// where dynamic dispatch is unsafe. Build-tag polymorphism is used instead.

package kmem

// PTEFlags is an architecture-neutral representation of page table entry attributes.
// Each platform's paging_*.go translates to/from native PTE format via
// platformPTEToFlags / platformFlagsToUserPTE / platformFlagsToKernelPTE.
type PTEFlags uint64

const (
	PF_Valid    PTEFlags = 1 << 0 // Entry is present/valid
	PF_Write    PTEFlags = 1 << 1 // Writable
	PF_User     PTEFlags = 1 << 2 // User-accessible (EL0/ring 3)
	PF_Execute  PTEFlags = 1 << 3 // Executable
	PF_Accessed PTEFlags = 1 << 4 // Hardware accessed bit
	PF_Dirty    PTEFlags = 1 << 5 // Hardware dirty bit
	PF_Device   PTEFlags = 1 << 6 // Device/uncacheable memory
	PF_Global   PTEFlags = 1 << 7 // Global (not ASID-tagged)
	PF_Pinned   PTEFlags = 1 << 8 // Software: do not evict
	PF_Shared   PTEFlags = 1 << 9 // Software: shared between shepherds
)

// Has returns true if all the specified flags are set.
//
//go:nosplit
func (f PTEFlags) Has(flags PTEFlags) bool {
	return f&flags == flags
}

// HasAny returns true if any of the specified flags are set.
//
//go:nosplit
func (f PTEFlags) HasAny(flags PTEFlags) bool {
	return f&flags != 0
}

// ElfFlagsFromPTEFlags converts arch-neutral PTEFlags to ELF program header flags
// (PF_R=4, PF_W=2, PF_X=1) for use with existing mapUserPageWithL0 etc.
//
//go:nosplit
func ElfFlagsFromPTEFlags(flags PTEFlags) uint32 {
	var elf uint32
	// Read is implied if valid
	if flags.Has(PF_Valid) {
		elf |= ELF_PF_R
	}
	if flags.Has(PF_Write) {
		elf |= ELF_PF_W
	}
	if flags.Has(PF_Execute) {
		elf |= ELF_PF_X
	}
	return elf
}
