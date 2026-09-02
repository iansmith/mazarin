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
	"mazzy/kmazarin/klog"
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
	// PD_FILE_BACKED pages are skipped: they are dual-mapped with a handler
	// shepherd that still has a live PTE. handleFlushReply releases them once
	// the handler finishes flushing. Releasing here would cause the handler
	// to write through a stale PTE into a reused page.
	spans.ForEach(func(start, length uint64) {
		end := start + length
		for va := uintptr(start); va < uintptr(end); va += PageSize {
			pa := WalkUserPageTableWithL0(va, l0PA)
			if pa == 0 {
				continue // Not mapped yet (demand paging)
			}
			if desc := GetPageDescriptor(pa); desc != nil && (desc.Flags&PD_FILE_BACKED) != 0 {
				continue // Handler still holds a live PTE; defer to handleFlushReply
			}
			// [MAZ-195] Unmap BEFORE release: never free a page while an
			// address space — even a dying one — still maps it. Releasing
			// with the leaf PTE live (it would otherwise survive until
			// Phase 2 destroys the L3 table) hands the page to a new owner
			// while the old mapping exists; that is benign today only
			// because the owner's threads no longer run, which nothing
			// enforces. Unmapping first also keeps Phase 2's walk exact —
			// it no longer sees this PTE.
			UnmapUserPageWithL0(va, l0PA)
			releasePageByPA(pa)
		}
	})

	// Phase 2: Walk the full page table hierarchy bottom-up and reclaim EVERY
	// process-owned leaf page plus the intermediate page-table pages.
	//
	// LEAK FIX: we always free leaves here (freeLeaves=true) and use the teardown
	// skip-rules (teardown=true). The old normal path passed freeLeaves=false on
	// the assumption that Phase 1 (the Spans walk) had already freed all leaves —
	// but the demand-paged user mmap / Go-heap region is tracked by the per-shepherd
	// BumpPointer, NOT in Spans, so it was never added there. Result: every shepherd
	// that died via the normal (non-deferred) path leaked its entire heap. Harmless
	// for long-lived shepherds; fatal under fork/exec churn (forkexectest spawns and
	// reaps children, each leaking tens of MB → buddy OOM, ~597K UserHeap pages).
	//
	// The page table is the authoritative record of what this process has mapped, so
	// walking it reclaims the heap regardless of Spans coverage or the owner tag on
	// the descriptor. teardown=true makes the leaf walk skip shared/pinned frames
	// (framebuffer, constraint block — RefCount<=0 or PD_PINNED) and force-free the
	// process-private PT pages (which are auto-pinned as PageKernelPT when built on
	// the kernel worker). Phase 1 above is now redundant but harmless: any leaf it
	// freed is RefCount<=0 by the time the leaf walk reaches it and is skipped.
	walkAndFreePageTablePages(l0PA, true, true)
	_ = freeLeaves // retained for ABI/call-site compatibility; reclamation is now unconditional
}

// releasePTPage frees a page-table page. When unpin is true it first clears
// PD_PINNED on the page so a process page table built on the kernel worker —
// whose PT pages get typed PageKernelPT (because pfContextShepherdID==0) and
// therefore auto-pinned by SetPageDescriptor — can still be reclaimed. This is
// safe only for page-table pages reached by walking a *process* L0: on ARM64
// those are entirely process-private (TTBR0 is user-only), and on x86_64/RISC-V
// the walk already skips kernel-shared L1 entries (platformIsSharedKernelL1).
// Leaf frames are released via releasePageByPA, which still honors PD_PINNED, so
// genuinely pinned leaves (framebuffer, constraint shared block) stay protected.
func releasePTPage(pa uintptr, unpin bool) bool {
	if unpin {
		if desc := GetPageDescriptor(pa); desc != nil {
			ClearPageDescriptorFlags(desc, PD_PINNED)
		}
	}
	return releasePageByPA(pa)
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
	// MAZ-15: the whole check→decrement→clear sequence runs under
	// pageDescLock. This path runs PREEMPTIBLE on thread 0 (deferred shepherd
	// cleanup); without the lock, a syscall's RefCount++ on the same
	// descriptor could interleave into the middle of the plain int16 RMW and
	// be lost — freeing a page that is still mapped elsewhere. The lock is
	// dropped before BuddyFreeTyped (lock order: never buddy inside
	// pageDescLock).
	pageDescLock.Lock()
	if desc.RefCount <= 0 {
		pageDescLock.Unlock()
		// Bug B family diagnostic: caller asked us to release a page whose
		// RefCount is already 0 (or negative, meaning a prior decrement
		// underflowed). Either: (a) something released this page already
		// without removing the caller's PTE, (b) a race where two threads
		// both saw RefCount=1 and both decremented past 0. Either is a
		// real bug we want to catch. Log loudly with type/owner/flags so
		// we can correlate against the share-and-release flow.
		// Criticalf (not Errf): this runs UNDER schedulerLock during shepherd
		// cleanup. Errf routes through the soft-IRQ console ring, which yields to
		// the scheduler (SaveThread0AndYield → schedulerLock.Lock) and deadlocks
		// re-entrantly. Criticalf polls the UART directly — no yield, no re-lock.
		klog.Criticalf("[kmem:UNDERFLOW]", " pa=%x refCount=%d type=%d owner=%d flags=%x\n",
			uint64(pa), int(desc.RefCount), int(desc.Type), int(desc.Owner), uint64(desc.Flags))
		return false // Already freed or untracked
	}
	if desc.Flags&PD_PINNED != 0 {
		pageDescLock.Unlock()
		return false // Pinned system pages (e.g. constraint shared block) are never freed
	}

	// Safety guard: file-backed pages are dual-mapped. The handler's PTE must be
	// removed first (done by handleFlushReply, which clears PD_FILE_BACKED before
	// calling ReleasePageByPA). Any other caller releasing a FILE_BACKED page has
	// a stale handler PTE → corruption. Block the free and log the site.
	if desc.Flags&PD_FILE_BACKED != 0 {
		pageDescLock.Unlock()
		klog.Criticalf("[kmem]", " BLOCKED: premature release of FILE_BACKED page pa=%x owner=%d\n",
			uint64(pa), int(desc.Owner))
		return false
	}

	desc.RefCount--
	if desc.RefCount > 0 {
		pageDescLock.Unlock()
		return false // Still referenced (shared IPC page, etc.)
	}

	// RefCount reached zero — capture type/order, clear the descriptor inline
	// (NOT via ClearPageDescriptor, which takes pageDescLock itself — the
	// spinlock is not reentrant), then release the lock before the buddy free.
	pageType := desc.Type
	order := int(desc.Order)
	desc.PA = 0
	desc.Type = 0
	desc.Owner = 0
	desc.RefCount = 0
	desc.Order = 0
	desc.Flags = 0
	pageDescLock.Unlock()

	BuddyFreeTyped(pa, order, pageType)
	return true
}

// walkAndFreePageTablePages walks the userspace half of the L0→L3 page table
// hierarchy bottom-up and frees intermediate page table pages (L3, L2, L1
// tables and the L0 root). When freeLeaves is true, also walks L3 entries and
// frees each leaf (data) page before freeing the L3 table itself — used by the
// deferred cleanup path where Phase 1 Spans walk freed nothing.
//
// When teardown is true (FreeProcessPageTable / process-teardown path):
//   - intermediate/L0 page-table pages are force-freed even if PD_PINNED (see
//     releasePTPage) — worker-built process PT pages get auto-pinned as
//     PageKernelPT and must still be reclaimed; and
//   - the freeLeaves walk frees only genuinely process-owned leaves, silently
//     skipping shared/mapped-in frames (framebuffer, constraint block) rather
//     than routing them through releasePageByPA (which would spam UNDERFLOW).
//
// When teardown is false (shepherd-exit via CleanupShepherdPages), behavior is
// unchanged: PT pages honor PD_PINNED and every leaf goes through releasePageByPA.
//
// Returns the total number of PT pages freed.
func walkAndFreePageTablePages(l0PA uintptr, freeLeaves bool, teardown bool) int {
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
								desc := GetPageDescriptor(leafPA)
								if desc != nil && (desc.Flags&PD_FILE_BACKED) != 0 {
									continue // Handler still holds PTE; defer to handleFlushReply
								}
								if teardown {
									// Process-teardown path: free only genuinely
									// process-owned leaves. Shared/mapped-in frames
									// (framebuffer, constraint block) are mapped by VA
									// into the process but not owned by it — they have
									// RefCount<=0 or PD_PINNED. Skip them silently;
									// routing them through releasePageByPA would spam the
									// UNDERFLOW bug-detector (the framebuffer alone is
									// thousands of frames) without freeing anything.
									if desc == nil || desc.RefCount <= 0 || (desc.Flags&PD_PINNED) != 0 {
										continue
									}
								}
								releasePageByPA(leafPA)
							}
						}
					}
				}

				// Free the L3 page table page itself.
				if releasePTPage(l3PA, teardown) {
					freed++
				}
			}
			// Free the L2 page table page itself.
			if releasePTPage(l2PA, teardown) {
				freed++
			}
		}
		// Free the L1 page table page itself.
		if releasePTPage(l1PA, teardown) {
			freed++
		}
	}
	// Free the L0 root page itself.
	if releasePTPage(l0PA, teardown) {
		freed++
	}

	return freed
}
