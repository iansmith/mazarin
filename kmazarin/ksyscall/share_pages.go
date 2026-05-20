package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// MaxTransferPages is the outer sanity ceiling on a single TransferPages call
// (32768 pages == 128 MB). The work is chunked internally — see transferChunkPages.
const MaxTransferPages = 32768

// transferChunkPages is the per-iteration validate+transfer batch size. IRQs are
// disabled for each chunk only, not across the full transfer, so a large transfer
// doesn't hold off timer IRQs for the whole duration.
const transferChunkPages = 4096

// MaxDMAClumpPages caps SyscallTransferDMAClump at a small clump size so the
// page-array can live on the g0 stack and the whole transfer fits inside a
// single IRQ-disabled critical section. Real DMA clumps are 1 page (UDP TX) up
// to a few pages (block I/O scratch); 64 pages is well past any plausible use.
const MaxDMAClumpPages = 64

// SyscallTransferPages transfers ownership of contiguous pages from the calling
// shepherd to a target shepherd. The pages are unmapped from the caller's address space,
// their ownership is updated, and they are mapped into the target's address space.
//
// Large transfers are chunked internally. The target VA range is allocated once up
// front and remains contiguous; each chunk validates+transfers under IRQs-disabled,
// with IRQs re-enabled between chunks.
//
// Args:
//
//	arg0 = targetPID (shepherd to transfer pages to)
//	arg1 = sourceVA  (start of contiguous page range in caller's address space)
//	arg2 = numPages  (number of 4KB pages to transfer, 1..MaxTransferPages)
//	arg3 = elfFlags  (ELF permission flags for target mapping; 0 = RW)
//
// Returns: target VA base on success, or negative errno on failure.
func SyscallTransferPages(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	targetPID := int16(arg0)
	sourceVA := uintptr(arg1)
	numPages := int(arg2)
	elfFlags := uint32(arg3)

	if numPages < 1 || numPages > MaxTransferPages {
		return -22 // EINVAL
	}
	if sourceVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	if targetPID == callerSID {
		return -22 // EINVAL
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetPID))
	if targetShepherd == nil {
		return -3 // ESRCH — no such process
	}
	if targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH — target has no address space
	}

	sourceL0PA := callerShepherd.PageTableL0PA

	// Allocate the entire target VA range up front so the caller sees a
	// single contiguous region. bumpAllocForShepherd is monotonic, so we
	// cannot recover this range on partial failure (fail-stop semantics).
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}
	targetShepherd.Spans.Add(targetVABase, totalSize)

	// PA scratch buffer sized for the full transfer (not just one chunk).
	// Each forward-pass chunk fills its window [chunkStart, chunkStart+chunkN)
	// and a cross-chunk rollback walks back through prior windows by the same
	// index, so the per-page PA must outlive the chunk that recorded it.
	//
	// Heap-allocated by this (splittable) outer; the nosplit per-chunk inner
	// only sees the slice header (24 B) on its frame and indexes into the
	// caller-owned backing array — no morestack risk inside the IRQ-off region.
	// Worst case is numPages == MaxTransferPages (32768) → 256 KB, allocated
	// per-syscall and GCed promptly. Real workloads use 1..a-few-dozen.
	pas := make([]uintptr, numPages)

	for chunkStart := 0; chunkStart < numPages; chunkStart += transferChunkPages {
		chunkN := transferChunkPages
		if chunkStart+chunkN > numPages {
			chunkN = numPages - chunkStart
		}
		errCode, failVA := transferPagesChunkInner(
			targetPID, callerSID, sourceL0PA,
			sourceVA, uintptr(targetVABase), chunkStart, chunkN, pas, elfFlags,
		)
		if errCode != 0 {
			switch errCode {
			case -14:
				klog.Errf("[IPC] TransferPages: page not mapped at VA %x (chunk %d)\n",
					uint64(failVA), chunkStart)
			case -1:
				klog.Errf("[IPC] TransferPages: page not owned by caller at PA %x (chunk %d)\n",
					uint64(failVA), chunkStart)
			case -12:
				klog.Errf("[IPC] TransferPages: MapPageInProcess failed at target VA %x (chunk %d) — chunk prefix + prior chunks orphaned\n",
					uint64(failVA), chunkStart)
			}
			// Fail-stop: target VA range, the target Spans entry, and any
			// already-transferred chunks are leaked. Rollback tracked as MAZ-37.
			return errCode
		}
	}

	// Remove the source span once after all chunks succeed.
	callerShepherd.Spans.Remove(uint64(sourceVA), totalSize)

	return int64(targetVABase)
}

// transferPagesChunkInner runs one chunk of SyscallTransferPages with IRQs
// disabled. The nosplit boundary keeps morestack from firing inside the
// IRQ-off region — important because Go's stack-growth machinery may take
// runtime locks or trigger GC work that is unsafe to run while DAIF.I is
// masked and we're mid-mutation of page tables and ownership descriptors.
//
// pas is the outer's heap-allocated scratch; only the first chunkN slots
// are touched. Fail-stop: on Pass 1 error the chunk's IRQ-off section is
// torn down with no pages transferred yet; rollback is out of scope.
//
// Returns (errCode int64, failVA uintptr). errCode is 0 on success or a
// negative errno; failVA is the address the caller wants to log (source VA
// for EFAULT, source PA for EPERM).
//
//go:nosplit
func transferPagesChunkInner(
	targetPID int16,
	callerSID int16,
	sourceL0PA uintptr,
	sourceVA uintptr,
	targetVABase uintptr,
	chunkStart int,
	chunkN int,
	pas []uintptr,
	elfFlags uint32,
) (int64, uintptr) {
	// Disable IRQs across this chunk's two passes to prevent async preemption
	// from letting another goroutine call exit_group between validate and
	// transfer — CleanupShepherdPages would free pages that Pass 1 already
	// validated, causing use-after-free in Pass 2.
	savedDAIF := saveAndDisableIRQs()

	// Pass 1: validate every page in this chunk is mapped and owned by caller.
	for i := 0; i < chunkN; i++ {
		va := sourceVA + uintptr(chunkStart+i)*kmem.PageSize
		pa := kmem.DemandMapUserPage(va, sourceL0PA)
		if pa == 0 {
			restoreIRQs(savedDAIF)
			return -14, va // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)
		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != callerSID {
			restoreIRQs(savedDAIF)
			return -1, pa // EPERM
		}
		pas[i] = pa
	}

	// Pass 2: unmap from source, transfer ownership, map into target. Fail-stop
	// on MapPageInProcess failure — the prior indices in this chunk plus all
	// completed chunks are orphaned (target-owned, no syscall return). Rollback
	// is MAZ-37; until then the caller's PR-visible signal is the -ENOMEM.
	for i := 0; i < chunkN; i++ {
		va := sourceVA + uintptr(chunkStart+i)*kmem.PageSize
		pa := pas[i]
		kmem.UnmapUserPageWithL0(va, sourceL0PA)
		kmem.TransferPageOwnership(pa, callerSID, targetPID)
		targetVA := targetVABase + uintptr(chunkStart+i)*kmem.PageSize
		if !kmem.MapPageInProcess(targetPID, targetVA, pa, elfFlags) {
			restoreIRQs(savedDAIF)
			return -12, targetVA // ENOMEM
		}
	}

	restoreIRQs(savedDAIF)
	return 0, 0
}

// SyscallTransferDMAClump transfers ownership of one whole DMA clump from the
// calling shepherd to a target shepherd. The clump is a MAZARIN_CONTIGUOUS
// allocation tracked in the caller's per-shepherd clump table; transferring it
// atomically:
//   - unmaps each page from the caller's address space,
//   - updates PageDescriptor.Owner to the target,
//   - maps each page into the target's address space at a contiguous bump-allocated
//     VA range,
//   - removes the source clump entry from the caller's clump table so the
//     death-cleanup path (CleanupShepherdDMAClumps / munmapClump) does not buddy-free
//     pages that net.elf now owns.
//
// This is the page-handoff primitive that NetIPC uses for client→net.elf TX
// pages: the client allocates a 1-page MAZARIN_CONTIGUOUS clump, writes the
// payload, then calls this to hand the page over. Net.elf does NOT register a
// clump entry on its side; it accesses the page by VA directly.
//
// Args:
//
//	arg0 = targetPID      (shepherd to transfer the clump to)
//	arg1 = clumpStartVA   (start VA of the clump in caller's address space —
//	                        must match an existing DMAClump.StartVA exactly)
//	arg2 = elfFlags       (ELF permission flags for target mapping; 0 = RW)
//
// Returns: target VA base on success, or negative errno on failure.
//
// Errors:
//
//	EINVAL  bad alignment or self-transfer
//	ESRCH   target shepherd does not exist or has no address space
//	ENOENT  no clump at clumpStartVA, or VA is in the middle of a clump
//	EBUSY   clump has in-flight I/O (cannot transfer until completion)
//	ENOMEM  target mapping failed mid-transfer; rollback restored caller's
//	        mappings; source clump entry preserved
//	EFAULT  source page unexpectedly unmapped (caller misuse / race)
//	EPERM   page not owned by caller (caller misuse / race)
//
// The IRQ-disabled critical section lives in transferDMAClumpInner (//go:nosplit).
// This wrapper holds the on-stack `var pas` scratch, the clump-table mutations,
// and all klog.Errf calls; the inner helper returns distinct errnos for each
// failure point along with the failing index for logging.
func SyscallTransferDMAClump(arg0, arg1, arg2, _, _, _ uint64) int64 {
	targetPID := int16(arg0)
	clumpStartVA := uintptr(arg1)
	elfFlags := uint32(arg2)

	if clumpStartVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	if targetPID == callerSID {
		return -22 // EINVAL
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetPID))
	if targetShepherd == nil {
		return -3 // ESRCH
	}
	if targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	// Resolve the clump under the clump lock so we know its index for removal.
	// We hold the lock only across the lookup; the actual page-transfer dance
	// runs IRQs-disabled (same as SyscallTransferPages) and the clump entry is
	// removed at the end under the lock again.
	callerShepherd.LockClumps()
	clumpIdx := callerShepherd.FindClumpIndexByVA(clumpStartVA)
	if clumpIdx < 0 {
		callerShepherd.UnlockClumps()
		klog.Errf("[IPC] TransferDMAClump: no clump at VA %x for SID=%d\n",
			uint64(clumpStartVA), callerSID)
		return -2 // ENOENT
	}
	clump := &callerShepherd.DMAClumps[clumpIdx]
	if clump.InFlight > 0 {
		callerShepherd.UnlockClumps()
		klog.Errf("[IPC] TransferDMAClump: clump at VA %x has in-flight I/O (InFlight=%d)\n",
			uint64(clumpStartVA), clump.InFlight)
		return -16 // EBUSY
	}
	numPages := clump.NumPages
	callerShepherd.UnlockClumps()

	if numPages < 1 || numPages > MaxDMAClumpPages {
		return -22 // EINVAL
	}

	sourceL0PA := callerShepherd.PageTableL0PA
	targetL0PA := targetShepherd.PageTableL0PA

	// Allocate the entire target VA range up front.
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}
	targetShepherd.Spans.Add(targetVABase, totalSize)

	// Stack-allocated PA scratch lives in this (splittable) frame; inner takes
	// it by pointer so it doesn't charge its own nosplit budget for 512 B.
	var pas [MaxDMAClumpPages]uintptr

	errCode, failIdx := transferDMAClumpInner(
		targetPID, callerSID, sourceL0PA, targetL0PA,
		&pas, numPages, clumpStartVA, uintptr(targetVABase), elfFlags,
	)
	if errCode != 0 {
		// Inner already rolled back any partial Pass-2 progress (ENOMEM path) and
		// restored IRQs. Drop the target Spans entry — no pages remain mapped
		// in the target's address space at this range. The bumpAlloc'd VA is
		// monotonic and not recoverable, but the Spans bookkeeping is.
		targetShepherd.Spans.Remove(targetVABase, totalSize)
		switch errCode {
		case -14:
			klog.Errf("[IPC] TransferDMAClump: page not mapped at VA %x (idx %d of %d)\n",
				uint64(clumpStartVA+uintptr(failIdx)*kmem.PageSize), failIdx, numPages)
		case -1:
			klog.Errf("[IPC] TransferDMAClump: page not owned by caller at PA %x (idx %d of %d)\n",
				uint64(pas[failIdx]), failIdx, numPages)
		case -12:
			klog.Errf("[IPC] TransferDMAClump: MapPageInProcess failed at target VA %x (idx %d of %d) — rolled back\n",
				uint64(targetVABase)+uint64(failIdx)*uint64(kmem.PageSize), failIdx, numPages)
		}
		return errCode
	}

	// Remove the source clump entry. The clump's pages are now owned by the
	// target, and the caller's Spans entry for this region is removed so
	// CleanupShepherdPages won't try to release pages we no longer own.
	callerShepherd.LockClumps()
	// Re-find by VA: another CPU may have done RemoveClump on a different
	// entry during the lock-released transfer, shifting our entry's index.
	if idx := callerShepherd.FindClumpIndexByVA(clumpStartVA); idx >= 0 {
		callerShepherd.RemoveClump(idx)
	}
	callerShepherd.UnlockClumps()
	callerShepherd.Spans.Remove(uint64(clumpStartVA), totalSize)

	return int64(targetVABase)
}

// transferDMAClumpInner runs the IRQ-disabled critical section of
// SyscallTransferDMAClump. Pass 1 validates source pages; Pass 2 unmaps source,
// transfers ownership, and maps target. On Pass-2 MapPageInProcess failure at
// index i, indices [0..i-1] are rolled back: target mapping dropped, ownership
// returned to caller, source mapping restored. If the rollback-side
// MapPageInProcess fails (orphan-page edge case), the kernel panics — silently
// leaking a page would let CleanupShepherdDMAClumps later BuddyFree an
// unowned page (the very UAF this rollback exists to prevent).
//
// Returns (errCode, failIdx). errCode is 0 on success or a negative errno;
// failIdx is the index where the failure occurred (meaningful only on error).
//
//go:nosplit
func transferDMAClumpInner(
	targetPID int16,
	callerSID int16,
	sourceL0PA uintptr,
	targetL0PA uintptr,
	pas *[MaxDMAClumpPages]uintptr,
	numPages int,
	clumpStartVA uintptr,
	targetVABase uintptr,
	elfFlags uint32,
) (int64, int) {
	savedDAIF := saveAndDisableIRQs()

	// Pass 1: validate every page is mapped and owned by caller. pas[i] is
	// stored before the ownership check so the outer wrapper's klog.Errf can
	// read pas[failIdx] for the EPERM case (else it would log PA 0x0).
	for i := 0; i < numPages; i++ {
		va := clumpStartVA + uintptr(i)*kmem.PageSize
		pa := kmem.DemandMapUserPage(va, sourceL0PA)
		if pa == 0 {
			restoreIRQs(savedDAIF)
			return -14, i // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)
		pas[i] = pa
		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != callerSID {
			restoreIRQs(savedDAIF)
			return -1, i // EPERM
		}
	}

	// Pass 2: unmap from source, transfer ownership, map into target. On failure,
	// roll back indices [0..i-1] to caller-owned + caller-mapped before
	// returning ENOMEM. The source clump entry stays intact so the caller's
	// CleanupShepherdDMAClumps continues to see correct ownership.
	for i := 0; i < numPages; i++ {
		va := clumpStartVA + uintptr(i)*kmem.PageSize
		pa := pas[i]
		kmem.UnmapUserPageWithL0(va, sourceL0PA)
		kmem.TransferPageOwnership(pa, callerSID, targetPID)
		targetVA := targetVABase + uintptr(i)*kmem.PageSize
		if !kmem.MapPageInProcess(targetPID, targetVA, pa, elfFlags) {
			// Roll back indices [0..i] in reverse. Index i had ownership
			// transferred but no target mapping committed (the map call
			// that just failed); j < i had both. The j<i guard handles the
			// asymmetry — we unmap target only where a forward map succeeded.
			//
			// NOTE: rollback restores caller mappings with `elfFlags` (the
			// target's intended flags), not the caller's original PTE flags.
			// Benign today because every DMA clump in tree is R+W and
			// elfFlags defaults to R+W when 0 — so rollback delivers the
			// same permissions the caller had. Real fix tracked as MAZ-39
			// (requires a kmem helper to extract PTE flags by VA + parallel
			// origFlags scratch in Pass 1).
			for j := i; j >= 0; j-- {
				if j < i {
					kmem.UnmapUserPageWithL0(targetVABase+uintptr(j)*kmem.PageSize, targetL0PA)
				}
				kmem.TransferPageOwnership(pas[j], targetPID, callerSID)
				if !kmem.MapPageInProcess(callerSID, clumpStartVA+uintptr(j)*kmem.PageSize, pas[j], elfFlags) {
					restoreIRQs(savedDAIF)
					KernelPanic("transferDMAClumpInner: rollback restore-mapping failed")
				}
			}
			restoreIRQs(savedDAIF)
			return -12, i // ENOMEM
		}
	}

	restoreIRQs(savedDAIF)
	return 0, 0
}

