// cleanup.go - Shepherd page cleanup on exit (Linux exit_mmap() equivalent)
//
// When the last thread of a shepherd exits, CleanupShepherdPages reclaims all
// physical pages owned by that shepherd:
//
//  Phase 1: Walk the shepherd's Spans (VMA list) and for each VA range walk
//           the user page tables to find and free every mapped leaf page.
//
//  Phase 2: Walk the full page table hierarchy (L0→L3) bottom-up and free
//           intermediate page table pages (L3, L2, L1 tables + L0 root).
//           Leaf pages are NOT freed here — Phase 1 handles all leaf pages.
//           The releasePageByPA RefCount guard catches any overlap, but we
//           skip leaf freeing entirely to avoid redundant work.
//
// Both phases use PageDescriptor refcounting: releasePageByPA decrements
// RefCount and frees (returns to buddy) only when it reaches zero, so
// shared pages (IPC, future Stage 6) are handled correctly.
//
// Design reference: design/MEMORY_OVERHAUL.md Stage 4

package kmem

import (
	"mazzy/kmazarin/proc"
	"unsafe"
)

// CleanupShepherdPages frees all physical pages belonging to the given shepherd.
// Called from releaseShepherdSchedLockHeld() after TLB shootdown, before the
// shepherd struct is zeroed.
//
// spans must point to the shepherd's LockedSpanGroup (read before zeroing the struct).
// l0PA is the physical address of the shepherd's L0 page table.
// freeLeaves controls whether Phase 2 also walks L3 entries to free leaf (data) pages.
// Normal path: false (Phase 1 Spans walk already frees all leaves).
// Deferred path: true (Spans are empty; Phase 2 must free leaves itself).
func CleanupShepherdPages(shepherdID proc.ShepherdId, spans *proc.LockedSpanGroup, l0PA uintptr, freeLeaves bool) {
	if l0PA == 0 {
		return // No page table allocated (e.g., shepherd that never ran)
	}

	// CRITICAL: On x86_64/RISC-V, the current CR3/SATP may still point to the
	// dying shepherd's page table (syscall entry doesn't change CR3). Phase 2
	// frees the shepherd's L0/PDPT pages — if CR3 still points to them, any TLB
	// miss after the free would walk through freed/reused pages → triple fault.
	// Switch to the kernel's own page table before freeing anything.
	// (ARM64 is immune: TTBR0 is user-only, kernel runs on TTBR1.)
	savedL0PA := readCurrentL0PA()
	kernelL0PA := GetKernelL0PA()
	if savedL0PA != kernelL0PA {
		SwitchTTBR0WithASID(kernelL0PA, 0)
	}

	// Phase 1: Walk Spans (our VMA list) to find all mapped VA regions and
	// free their leaf pages. Fast path: skips sparse (unmapped) ranges.
	spans.ForEach(func(start, length uint64) {
		end := start + length
		for va := uintptr(start); va < uintptr(end); va += PageSize {
			pa := WalkUserPageTableWithL0(va, l0PA)
			if pa == 0 {
				continue // Not mapped yet (demand paging)
			}
			releasePageByPA(pa)
		}
	})

	// Phase 2: Walk the full page table hierarchy bottom-up.
	// Frees all intermediate page table pages (L1/L2/L3 tables + L0 root).
	// When freeLeaves is true, also frees leaf (data) pages by walking L3 entries
	// before freeing the L3 table — used by the deferred cleanup path where Spans
	// is empty and Phase 1 freed nothing.
	walkAndFreePageTablePages(l0PA, freeLeaves)
}

// ReleasePageByPA decrements the refcount for the physical page at pa and
// frees it back to the buddy allocator when refcount reaches zero.
// Returns true if the page was freed.
// Safe to call for PAs outside the pool (returns false).
func ReleasePageByPA(pa uintptr) bool {
	return releasePageByPA(pa)
}

// releasePageByPA is the internal implementation of ReleasePageByPA.
// Also used by walkAndFreePageTablePages for PT page cleanup.
func releasePageByPA(pa uintptr) bool {
	desc := GetPageDescriptor(pa)
	if desc == nil {
		return false // Not in pool (diplomat-mapped, MMIO, or out of range)
	}
	if desc.RefCount <= 0 {
		return false // Already freed or untracked
	}

	desc.RefCount--
	if desc.RefCount > 0 {
		return false // Still referenced (shared IPC page, etc.)
	}

	// RefCount reached zero — capture type/order before clearing.
	pageType := desc.Type
	order := int(desc.Order)

	// Clear the descriptor first, then return to buddy (no window where
	// RefCount=0 but page is not yet in the free list matters here because
	// we run with the scheduler lock held during shepherd cleanup).
	ClearPageDescriptor(pa)
	BuddyFreeTyped(pa, order, pageType)
	return true
}

// walkAndFreePageTablePages walks the userspace half of the L0→L3 page table
// hierarchy bottom-up and frees intermediate page table pages (L3, L2, L1
// tables and the L0 root). When freeLeaves is true, also walks L3 entries and
// frees each leaf (data) page before freeing the L3 table itself — used by the
// deferred cleanup path where Phase 1 Spans walk freed nothing.
//
// Returns the total number of PT pages freed.
func walkAndFreePageTablePages(l0PA uintptr, freeLeaves bool) int {
	freed := 0

	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}

	// Walk only the userspace half:
	//   ARM64:        all 512 L0 entries (TTBR0 is entirely userspace)
	//   x86_64/RISC-V: only 256 entries (upper half [256-511] is kernel)
	maxEntry := platformUserL0MaxEntry()

	for i := 0; i < maxEntry; i++ {
		l0e := *(*uint64)(unsafe.Pointer(l0VA + uintptr(i)*8))
		if !pteIsValid(l0e) {
			continue
		}

		l1PA := pteExtractPA(l0e)
		l1VA := paToVAOrCache(l1PA)
		if l1VA == 0 {
			continue
		}

		for j := 0; j < 512; j++ {
			l1e := *(*uint64)(unsafe.Pointer(l1VA + uintptr(j)*8))
			if !pteIsValid(l1e) {
				continue
			}
			if isBlockEntry(l1e, 1) {
				continue // 1GB block mapping (should not appear in userspace)
			}
			// On x86_64/RISC-V, L0[0]'s L1 table has entries shared with
			// the kernel (PDPT[1] on x86_64, L2[1+] on RISC-V). These
			// point to kernel PD/PT pages used by ALL shepherds. Freeing
			// them destroys kernel mappings and causes triple faults.
			if i == 0 && platformIsSharedKernelL1(j) {
				continue
			}

			l2PA := pteExtractPA(l1e)
			l2VA := paToVAOrCache(l2PA)
			if l2VA == 0 {
				continue
			}

			for k := 0; k < 512; k++ {
				l2e := *(*uint64)(unsafe.Pointer(l2VA + uintptr(k)*8))
				if !pteIsValid(l2e) {
					continue
				}
				if isBlockEntry(l2e, 2) {
					continue // 2MB block mapping (kernel huge page, skip)
				}

				l3PA := pteExtractPA(l2e)
				// When freeLeaves is true (deferred-cleanup path), walk
				// L3 entries and free each leaf page before freeing the
				// L3 table. On the normal path Phase 1 already freed all
				// leaves via Spans, so we skip this to avoid redundant work.
				if freeLeaves {
					l3VA := paToVAOrCache(l3PA)
					if l3VA != 0 {
						for l := 0; l < 512; l++ {
							l3e := *(*uint64)(unsafe.Pointer(l3VA + uintptr(l)*8))
							if pteIsValid(l3e) {
								leafPA := pteExtractPA(l3e)
								releasePageByPA(leafPA)
							}
						}
					}
				}

				// Free the L3 page table page itself.
				if releasePageByPA(l3PA) {
					freed++
				}
			}
			// Free the L2 page table page itself.
			if releasePageByPA(l2PA) {
				freed++
			}
		}
		// Free the L1 page table page itself.
		if releasePageByPA(l1PA) {
			freed++
		}
	}
	// Free the L0 root page itself.
	if releasePageByPA(l0PA) {
		freed++
	}

	return freed
}
