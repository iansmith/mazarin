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
	// cannot recover this range on partial failure (see rollback note below).
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}
	targetShepherd.Spans.Add(targetVABase, totalSize)

	// Reusable PA scratch buffer for per-chunk Pass 1 → Pass 2 hand-off.
	// Heap-allocated (this function is not nosplit) to avoid a large stack array.
	pas := make([]uintptr, transferChunkPages)

	for chunkStart := 0; chunkStart < numPages; chunkStart += transferChunkPages {
		chunkN := transferChunkPages
		if chunkStart+chunkN > numPages {
			chunkN = numPages - chunkStart
		}

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
				klog.Errf("[IPC] TransferPages: page not mapped at VA %x (chunk %d)\n", uint64(va), chunkStart)
				// Fail-stop: target VA range and any already-transferred chunks
				// are leaked. TODO: best-effort rollback.
				return -14 // EFAULT
			}
			pa = pa &^ (kmem.PageSize - 1)
			desc := kmem.GetPageDescriptor(pa)
			if desc == nil || desc.Owner != callerSID {
				restoreIRQs(savedDAIF)
				klog.Errf("[IPC] TransferPages: page not owned by caller at PA %x (chunk %d)\n", uint64(pa), chunkStart)
				return -1 // EPERM
			}
			pas[i] = pa
		}

		// Pass 2: unmap from source, transfer ownership, map into target.
		for i := 0; i < chunkN; i++ {
			va := sourceVA + uintptr(chunkStart+i)*kmem.PageSize
			pa := pas[i]
			kmem.UnmapUserPageWithL0(va, sourceL0PA)
			kmem.TransferPageOwnership(pa, callerSID, targetPID)
			targetVA := uintptr(targetVABase) + uintptr(chunkStart+i)*kmem.PageSize
			kmem.MapPageInProcess(targetPID, targetVA, pa, elfFlags)
		}

		restoreIRQs(savedDAIF)
	}

	// Remove the source span once after all chunks succeed.
	callerShepherd.Spans.Remove(uint64(sourceVA), totalSize)

	return int64(targetVABase)
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
//	ENOMEM  target VA allocation failed
//	EFAULT  source page unexpectedly unmapped (caller misuse / race)
//	EPERM   page not owned by caller (caller misuse / race)
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
	clumpIdx := int32(-1)
	for i := int32(0); i < callerShepherd.NumDMAClumps; i++ {
		if callerShepherd.DMAClumps[i].StartVA == clumpStartVA {
			clumpIdx = i
			break
		}
	}
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

	// Allocate the entire target VA range up front.
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}
	targetShepherd.Spans.Add(targetVABase, totalSize)

	// Stack-allocated PA scratch — fits because numPages ≤ MaxDMAClumpPages.
	// The full transfer runs inside one IRQ-disabled section: bounded clump size
	// means we don't need the chunked loop SyscallTransferPages uses to keep
	// timer IRQs responsive on multi-MB transfers.
	var pas [MaxDMAClumpPages]uintptr

	savedDAIF := saveAndDisableIRQs()

	// Pass 1: validate every page is mapped and owned by caller.
	for i := 0; i < numPages; i++ {
		va := clumpStartVA + uintptr(i)*kmem.PageSize
		pa := kmem.DemandMapUserPage(va, sourceL0PA)
		if pa == 0 {
			restoreIRQs(savedDAIF)
			klog.Errf("[IPC] TransferDMAClump: page not mapped at VA %x\n", uint64(va))
			return -14 // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)
		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != callerSID {
			restoreIRQs(savedDAIF)
			klog.Errf("[IPC] TransferDMAClump: page not owned by caller at PA %x\n", uint64(pa))
			return -1 // EPERM
		}
		pas[i] = pa
	}

	// Pass 2: unmap from source, transfer ownership, map into target.
	for i := 0; i < numPages; i++ {
		va := clumpStartVA + uintptr(i)*kmem.PageSize
		pa := pas[i]
		kmem.UnmapUserPageWithL0(va, sourceL0PA)
		kmem.TransferPageOwnership(pa, callerSID, targetPID)
		targetVA := uintptr(targetVABase) + uintptr(i)*kmem.PageSize
		kmem.MapPageInProcess(targetPID, targetVA, pa, elfFlags)
	}

	restoreIRQs(savedDAIF)

	// Remove the source clump entry. The clump's pages are now owned by the
	// target, and the caller's Spans entry for this region is removed so
	// CleanupShepherdPages won't try to release pages we no longer own.
	callerShepherd.LockClumps()
	// Re-find the clump by VA since the index may have shifted if another
	// clump was removed during the (IRQ-disabled but lock-released) transfer.
	idx := int32(-1)
	for i := int32(0); i < callerShepherd.NumDMAClumps; i++ {
		if callerShepherd.DMAClumps[i].StartVA == clumpStartVA {
			idx = i
			break
		}
	}
	if idx >= 0 {
		callerShepherd.RemoveClump(idx)
	}
	callerShepherd.UnlockClumps()
	callerShepherd.Spans.Remove(uint64(clumpStartVA), totalSize)

	return int64(targetVABase)
}

