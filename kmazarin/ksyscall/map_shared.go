package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// SyscallMapSharedPage creates a shared mapping of a page owned by another shepherd
// into the calling shepherd's address space. The page's refcount is incremented and
// PD_SHARED flag is set. Both shepherds can then access the same physical page.
//
// Args:
//
//	arg0 = ownerSID  (shepherd that owns the page)
//	arg1 = ownerVA   (VA of the page in the owner's address space)
//	arg2 = elfFlags  (ELF permission flags for caller's mapping; 0 = RW)
//
// Returns: caller VA on success, or negative errno on failure.
func SyscallMapSharedPage(arg0, arg1, arg2, _, _, _ uint64) int64 {
	ownerSID := int16(arg0)
	ownerVA := uintptr(arg1)
	elfFlags := uint32(arg2)

	// Validate ownerVA is page-aligned
	if ownerVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	// Get caller shepherd
	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	// Look up owner shepherd
	ownerShepherd := proc.FindShepherdBySID(proc.ShepherdId(ownerSID))
	if ownerShepherd == nil {
		return -3 // ESRCH — no such process
	}
	if ownerShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH — owner has no address space
	}

	// Resolve PA from owner's page table
	pa := kmem.WalkUserPageTableWithL0(ownerVA, ownerShepherd.PageTableL0PA)
	if pa == 0 {
		klog.Errf("[IPC] MapSharedPage: page not mapped in owner at VA %x\n", uint64(ownerVA))
		return -14 // EFAULT
	}
	// Strip page offset
	pa = pa &^ (kmem.PageSize - 1)

	// Verify ownership
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != ownerSID {
		klog.Errf("[IPC] MapSharedPage: page not owned by specified shepherd at PA %x\n", uint64(pa))
		return -1 // EPERM
	}

	// Increment refcount and mark as shared (locked cluster — MAZ-15)
	kmem.RefSharedPage(desc, kmem.PD_SHARED)

	// Allocate caller VA
	callerVA := bumpAllocForShepherd(callerShepherd, uint64(kmem.PageSize))
	if callerVA == 0 {
		// Roll back refcount
		kmem.UnrefSharedPage(desc, kmem.PD_SHARED)
		return -12 // ENOMEM
	}

	// Add span to caller
	callerShepherd.Spans.Add(callerVA, uint64(kmem.PageSize))

	// Map into caller's address space
	if !kmem.MapPageInProcess(callerSID, uintptr(callerVA), pa, elfFlags) {
		// Roll back
		callerShepherd.Spans.Remove(callerVA, uint64(kmem.PageSize))
		kmem.UnrefSharedPage(desc, kmem.PD_SHARED)
		return -12 // ENOMEM
	}

	return int64(callerVA)
}

// SyscallShareNetPageWithClient maps a page owned by the caller (intended to be
// net.elf) into a client shepherd's address space. The caller retains ownership;
// the page's RefCount is incremented and both PD_SHARED and PD_NET_OWNED_SHARED
// are set on the PageDescriptor. The client can read/write the page, but it
// survives the client's death: as long as the caller holds its own mapping,
// RefCount remains ≥ 1 and the page is not buddy-freed.
//
// PD_NET_OWNED_SHARED distinguishes net.elf-owned shared pages from regular
// PD_SHARED mappings so a future client-death notification path can route a
// reclaim callback to net.elf. In v1 the bit is informational only.
//
// Direction is flipped from SyscallMapSharedPage: caller is the *owner*, arg0
// is the *receiving* client. The page is resolved via the caller's own L0.
//
// Args:
//
//	arg0 = clientSID  (shepherd to share the page into)
//	arg1 = ownerVA    (VA of the page in the caller's address space)
//	arg2 = elfFlags   (ELF permission flags for client's mapping; 0 = RW)
//
// Returns: client VA on success, or negative errno on failure.
func SyscallShareNetPageWithClient(arg0, arg1, arg2, _, _, _ uint64) int64 {
	clientSID := int16(arg0)
	ownerVA := uintptr(arg1)
	elfFlags := uint32(arg2)

	if ownerVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	if clientSID == callerSID {
		return -22 // EINVAL — sharing with self is meaningless
	}

	clientShepherd := proc.FindShepherdBySID(proc.ShepherdId(clientSID))
	if clientShepherd == nil {
		return -3 // ESRCH
	}
	if clientShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	// Resolve PA from caller's (owner's) page table.
	pa := kmem.WalkUserPageTableWithL0(ownerVA, callerShepherd.PageTableL0PA)
	if pa == 0 {
		klog.Errf("[IPC] ShareNetPageWithClient: page not mapped in caller at VA %x\n", uint64(ownerVA))
		return -14 // EFAULT
	}
	pa = pa &^ (kmem.PageSize - 1)

	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != callerSID {
		klog.Errf("[IPC] ShareNetPageWithClient: page not owned by caller at PA %x\n", uint64(pa))
		return -1 // EPERM
	}

	const sharedFlags = uint8(kmem.PD_SHARED | kmem.PD_NET_OWNED_SHARED)
	kmem.RefSharedPage(desc, sharedFlags)

	clientVA := bumpAllocForShepherd(clientShepherd, uint64(kmem.PageSize))
	if clientVA == 0 {
		unrefShared(desc, sharedFlags)
		return -12 // ENOMEM
	}

	clientShepherd.Spans.Add(clientVA, uint64(kmem.PageSize))

	if !kmem.MapPageInProcess(clientSID, uintptr(clientVA), pa, elfFlags) {
		clientShepherd.Spans.Remove(clientVA, uint64(kmem.PageSize))
		unrefShared(desc, sharedFlags)
		return -12 // ENOMEM
	}

	return int64(clientVA)
}

// unrefShared rolls back the RefCount bump + flag set performed at the start
// of a share path when a subsequent step fails. The flags are only cleared
// once the page is no longer shared (RefCount back to 1, the owner's own
// mapping). Delegates to the locked kmem cluster (MAZ-15).
func unrefShared(desc *kmem.PageDescriptor, flags uint8) {
	kmem.UnrefSharedPage(desc, flags)
}

// SyscallSharePagesWithTarget maps a range of the caller's pages into a target
// shepherd's address space as shared pages. The caller retains ownership; refcounts
// are incremented and PD_SHARED is set on each page. Physical pages need not be
// contiguous — only the VA ranges are contiguous in both address spaces.
//
// Args:
//
//	arg0 = targetSID
//	arg1 = callerVA   (page-aligned start of contiguous VA range in caller's space)
//	arg2 = numPages
//
// Returns: target VA base on success, or negative errno on failure.
func SyscallSharePagesWithTarget(arg0, arg1, arg2, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	callerVA := uintptr(arg1)
	numPages := int(arg2)

	if numPages <= 0 || numPages > 4096 {
		return -22 // EINVAL
	}
	if callerVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	callerSID := int16(callerShepherd.PID)

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetSID))
	if targetShepherd == nil {
		return -3 // ESRCH
	}
	if targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	// Allocate contiguous VA range in target.
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}

	// rollbackShare undoes the mapping and RefCount++/PD_SHARED bump for the
	// first n pages already shared into the target. Unwinds in reverse order.
	rollbackShare := func(n int) {
		for j := n - 1; j >= 0; j-- {
			dstVA := uintptr(targetVABase) + uintptr(j)*kmem.PageSize
			kmem.UnmapUserPageWithL0(dstVA, targetShepherd.PageTableL0PA)
			rollPA := kmem.WalkUserPageTableWithL0(
				callerVA+uintptr(j)*kmem.PageSize, callerShepherd.PageTableL0PA)
			rollPA = rollPA &^ (kmem.PageSize - 1)
			if d := kmem.GetPageDescriptor(rollPA); d != nil {
				unrefShared(d, kmem.PD_SHARED)
			}
		}
	}

	// Map each page from caller into target.
	for i := 0; i < numPages; i++ {
		srcVA := callerVA + uintptr(i)*kmem.PageSize
		pa := kmem.WalkUserPageTableWithL0(srcVA, callerShepherd.PageTableL0PA)
		if pa == 0 {
			rollbackShare(i)
			klog.Errf("[IPC] SharePagesWithTarget: page not mapped in caller at VA %x\n", uint64(srcVA))
			return -14 // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)

		desc := kmem.GetPageDescriptor(pa)
		// Allow sharing if the caller owns the page OR if the page is already
		// shared and the caller has a valid mapping (chained share: A→B, B→C).
		// WalkUserPageTableWithL0 above confirmed the page is in the caller's PT.
		if desc == nil || (desc.Owner != callerSID && desc.Flags&kmem.PD_SHARED == 0) {
			rollbackShare(i)
			klog.Errf("[IPC] SharePagesWithTarget: caller has no share rights at PA %x\n", uint64(pa))
			return -1 // EPERM
		}

		kmem.RefSharedPage(desc, kmem.PD_SHARED)

		dstVA := uintptr(targetVABase) + uintptr(i)*kmem.PageSize
		if !kmem.MapPageInProcess(targetSID, dstVA, pa, 0) {
			unrefShared(desc, kmem.PD_SHARED)
			rollbackShare(i)
			return -12 // ENOMEM
		}
	}

	// Add single span covering all target pages.
	targetShepherd.Spans.Add(targetVABase, totalSize)

	return int64(targetVABase)
}

// SyscallUnshareFromTarget revokes a previously-established shared mapping
// in a target shepherd's address space. Inverse of SyscallSharePagesWithTarget.
// The page owner OR any intermediate holder (a shepherd with a valid shared
// mapping — PD_SHARED set and page is in caller's PT) may call unshare. This
// symmetry with SharePagesWithTarget's chained-share permission enables the
// A→B→C release chain: when C releases to B, B calls UnshareFromTarget(C),
// even though B is not the original owner. Unmaps from the target's PT (with
// TLB flush), decrements RefCount, clears PD_SHARED when RefCount reaches the
// owner-only floor. The caller's own mapping is left intact.
//
// MAZ-53: enables explicit Release semantics for Mode 2 shares so consumers
// don't have to rely on process death for cleanup.
//
// Args:
//
//	arg0 = targetSID
//	arg1 = targetVA   (page-aligned start of the consumer's shared region)
//	arg2 = numPages
//	arg3 = callerVA   (page-aligned start in caller's own address space;
//	                   0 for page owners; non-owners must supply this as a
//	                   proof-of-possession credential — kernel walks the
//	                   caller's PT to confirm the PA matches the target's PA)
//
// Returns: 0 on success, or negative errno on failure. On per-page failure
// mid-loop, prior unmaps are rolled back by re-mapping the pages into the
// target via MapPageInProcess; if rollback itself fails the target is left
// partially-unmapped and a klog error records the inconsistent state.
func SyscallUnshareFromTarget(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	targetSID := int16(arg0)
	targetVA := uintptr(arg1)
	numPages := int(arg2)
	callerVA := uintptr(arg3)

	if numPages <= 0 || numPages > 4096 {
		return -22 // EINVAL
	}
	if targetVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	callerSID := int16(callerShepherd.PID)

	if targetSID == callerSID {
		return -22 // EINVAL — unshare-from-self is meaningless
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetSID))
	if targetShepherd == nil {
		return -3 // ESRCH
	}
	if targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}
	targetL0PA := targetShepherd.PageTableL0PA

	// Track each successfully-unmapped page so we can rollback on per-page
	// failure mid-loop. Caps at the 4096-page hard limit from the EINVAL
	// check above; ~32 bytes per entry.
	type unmapped struct {
		dstVA    uintptr
		pa       uintptr
		refCount int16
		flags    uint8
	}
	rolled := make([]unmapped, 0, numPages)

	rollback := func() {
		// Re-map each prior unmap into the target. Best-effort: if a
		// re-map fails the target is left inconsistent and a klog error
		// records it; we don't recurse into rollback-of-rollback.
		for _, u := range rolled {
			if d := kmem.GetPageDescriptor(u.pa); d != nil {
				kmem.RestorePageShareState(d, u.refCount, u.flags)
			}
			if !kmem.MapPageInProcess(targetSID, u.dstVA, u.pa, 0) {
				klog.Errf("[IPC] UnshareFromTarget: rollback re-map failed at VA %x PA %x — target left inconsistent\n",
					uint64(u.dstVA), uint64(u.pa))
			}
		}
	}

	// Walk + unmap each page. Single-pass with rollback.
	for i := 0; i < numPages; i++ {
		dstVA := targetVA + uintptr(i)*kmem.PageSize

		// Find the PA the target's PT maps this VA to.
		pa := kmem.WalkUserPageTableWithL0(dstVA, targetL0PA)
		if pa == 0 {
			klog.Errf("[IPC] UnshareFromTarget: target has no mapping at VA %x\n", uint64(dstVA))
			rollback()
			return -14 // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)

		desc := kmem.GetPageDescriptor(pa)
		// Allow unshare if caller owns the page OR is an intermediate holder
		// (PD_SHARED set, caller has a valid mapping — chained share B unsharing C).
		if desc == nil || (desc.Owner != callerSID && desc.Flags&kmem.PD_SHARED == 0) {
			klog.Errf("[IPC] UnshareFromTarget: caller %d has no unshare rights at PA %x\n",
				callerSID, uint64(pa))
			rollback()
			return -1 // EPERM
		}

		// Proof-of-possession for non-owners: verify the caller's own mapping
		// covers the same physical page. This prevents a shepherd that merely
		// knows a ShareID (or guesses PD_SHARED) from revoking a mapping it
		// doesn't actually hold. Owners skip this — ownership is sufficient.
		if desc.Owner != callerSID {
			if callerVA == 0 {
				klog.Errf("[IPC] UnshareFromTarget: non-owner caller %d supplied no callerVA at PA %x\n",
					callerSID, uint64(pa))
				rollback()
				return -1 // EPERM
			}
			callerPageVA := callerVA + uintptr(i)*kmem.PageSize
			callerPagePA := kmem.WalkUserPageTableWithL0(callerPageVA, callerShepherd.PageTableL0PA)
			callerPagePA = callerPagePA &^ (kmem.PageSize - 1)
			if callerPagePA != pa {
				klog.Errf("[IPC] UnshareFromTarget: caller %d VA %x maps PA %x != target PA %x\n",
					callerSID, uint64(callerPageVA), uint64(callerPagePA), uint64(pa))
				rollback()
				return -1 // EPERM
			}
		}

		// Snapshot pre-state, then unmap. Snapshot before the descriptor mutation
		// so rollback can restore RefCount/Flags exactly.
		snap := unmapped{
			dstVA:    dstVA,
			pa:       pa,
			refCount: desc.RefCount,
			flags:    desc.Flags,
		}

		// Unmap from target's PT (handles TLB invalidation).
		if kmem.UnmapUserPageWithL0(dstVA, targetL0PA) == 0 {
			klog.Errf("[IPC] UnshareFromTarget: UnmapUserPageWithL0 failed at VA %x\n",
				uint64(dstVA))
			rollback()
			return -14 // EFAULT
		}
		rolled = append(rolled, snap)

		// Decrement RefCount; clear PD_SHARED if dropping to owner-only.
		unrefShared(desc, kmem.PD_SHARED)
	}

	return 0
}
