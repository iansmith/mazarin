// teardown.go — inverse-teardown primitives for a partially-initialized
// process address space (MAZ-108).
//
// CreateProcessPageTable + the loadELF mapping path build a process address
// space incrementally (L0 root → on-demand L1/L2/L3 tables → segment/stack
// leaf frames → framebuffer/constraint mappings). When a late init step fails
// (framebuffer/constraint map, loadELF), the caller must release everything
// allocated so far. Before this file, kmem had no inverse of
// CreateProcessPageTable, so DoCloneExecWork / DoRunShepherdWork leaked the
// page table + mapped frames on every such failure (the MAZ-75 "best-effort"
// teardown gap, marked TODO(MAZ-108)).
//
// FreeProcessPageTable is that inverse. It reuses walkAndFreePageTablePages
// (the deferred-cleanup walk already used by CleanupShepherdPages) in its
// teardown mode (freeLeaves=true, teardown=true), which:
//   - frees all intermediate L1/L2/L3 tables + the L0 root, force-unpinning
//     PT pages first (worker-built process PT pages get auto-pinned as
//     PageKernelPT — see releasePTPage),
//   - frees every genuinely process-owned leaf frame, refcount-aware so
//     shared-text-cache frames are decremented rather than freed,
//   - silently skips PD_PINNED / unowned leaf frames (framebuffer + constraint
//     shared block) — those are mapped in by VA but not owned by the process.
// So a single FreeProcessPageTable(l0PA) call reclaims the whole partial
// address space without a separate per-segment undo handle.

package kmem

// FreeProcessPageTable releases a process page table built by
// CreateProcessPageTable, along with every frame currently mapped through it
// (segments, stack, on-demand L1/L2/L3 tables). It is the inverse of
// CreateProcessPageTable for the partial-initialization teardown path.
//
// Safe to call on a partially-initialized table (unset L1/L2/L3 entries are
// skipped via pteIsValid) and on l0PA == 0 (no-op). PD_PINNED leaf frames
// (framebuffer, constraint shared block) and refcounted shared frames are NOT
// freed — only the process-private PT pages and process-owned leaves are. Must
// NOT be called on a page table the current CPU is actively translating through
// (the caller — a kernel worker building a child it never switched to — is not).
//
// Runs on the kernel worker (heap-OK), like CleanupShepherdPages — NOT
// //go:nosplit (walkAndFreePageTablePages and releasePageByPA/klog are not
// nosplit-safe).
func FreeProcessPageTable(l0PA uintptr) {
	if l0PA == 0 {
		return // Nothing allocated (e.g. CreateProcessPageTable returned 0).
	}

	// Never free a page table the current CPU is translating through. On
	// x86_64/RISC-V a TLB miss after the free could walk freed/reused pages.
	// The partial-init caller is a kernel worker that built this child L0 but
	// never switched to it, so this is normally a no-op (savedL0PA ==
	// kernelL0PA); mirror CleanupShepherdPages's guard for safety.
	// (ARM64 is immune: TTBR0 is user-only, kernel runs on TTBR1.)
	savedL0PA := readCurrentL0PA()
	kernelL0PA := GetKernelL0PA()
	if savedL0PA != kernelL0PA {
		SwitchTTBR0WithASID(kernelL0PA, 0)
	}

	// freeLeaves=true, teardown=true: there is no Spans list for a partially-
	// initialized process, so the page-table walk itself enumerates every mapped
	// frame — ELF segments, user stack, and the on-demand L1/L2/L3 tables — plus
	// the L0 root. teardown=true force-unpins the auto-pinned PT pages so they
	// reclaim, while leaf handling stays refcount-aware and skips PD_PINNED /
	// unowned frames (framebuffer + constraint shared block). This single call
	// therefore reclaims the whole partial address space without a separate
	// per-segment undo handle.
	walkAndFreePageTablePages(l0PA, true, true)
}
