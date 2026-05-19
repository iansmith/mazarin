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

	// Increment refcount and mark as shared
	desc.RefCount++
	desc.Flags |= kmem.PD_SHARED

	// Allocate caller VA
	callerVA := bumpAllocForShepherd(callerShepherd, uint64(kmem.PageSize))
	if callerVA == 0 {
		// Roll back refcount
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	// Add span to caller
	callerShepherd.Spans.Add(callerVA, uint64(kmem.PageSize))

	// Map into caller's address space
	if !kmem.MapPageInProcess(callerSID, uintptr(callerVA), pa, elfFlags) {
		// Roll back
		callerShepherd.Spans.Remove(callerVA, uint64(kmem.PageSize))
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
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
	desc.RefCount++
	desc.Flags |= sharedFlags

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
// mapping).
func unrefShared(desc *kmem.PageDescriptor, flags uint8) {
	desc.RefCount--
	if desc.RefCount <= 1 {
		desc.Flags &^= flags
	}
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

	// Map each page from caller into target.
	for i := 0; i < numPages; i++ {
		srcVA := callerVA + uintptr(i)*kmem.PageSize
		pa := kmem.WalkUserPageTableWithL0(srcVA, callerShepherd.PageTableL0PA)
		if pa == 0 {
			// Rollback pages mapped so far.
			for j := 0; j < i; j++ {
				rollPA := kmem.WalkUserPageTableWithL0(
					callerVA+uintptr(j)*kmem.PageSize, callerShepherd.PageTableL0PA)
				rollPA = rollPA &^ (kmem.PageSize - 1)
				if d := kmem.GetPageDescriptor(rollPA); d != nil {
					d.RefCount--
					if d.RefCount <= 1 {
						d.Flags &^= kmem.PD_SHARED
					}
				}
				// Note: we don't unmap from target here because demand-paging
				// may not have instantiated all intermediate page table entries.
				// The pages will be cleaned up when the target exits.
			}
			klog.Errf("[IPC] SharePagesWithTarget: page not mapped in caller at VA %x\n", uint64(srcVA))
			return -14 // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)

		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != callerSID {
			// Rollback.
			for j := 0; j < i; j++ {
				rollPA := kmem.WalkUserPageTableWithL0(
					callerVA+uintptr(j)*kmem.PageSize, callerShepherd.PageTableL0PA)
				rollPA = rollPA &^ (kmem.PageSize - 1)
				if d := kmem.GetPageDescriptor(rollPA); d != nil {
					d.RefCount--
					if d.RefCount <= 1 {
						d.Flags &^= kmem.PD_SHARED
					}
				}
			}
			klog.Errf("[IPC] SharePagesWithTarget: page not owned by caller at PA %x\n", uint64(pa))
			return -1 // EPERM
		}

		desc.RefCount++
		desc.Flags |= kmem.PD_SHARED

		dstVA := uintptr(targetVABase) + uintptr(i)*kmem.PageSize
		if !kmem.MapPageInProcess(targetSID, dstVA, pa, 0) {
			// Rollback this page and all prior.
			desc.RefCount--
			if desc.RefCount <= 1 {
				desc.Flags &^= kmem.PD_SHARED
			}
			for j := 0; j < i; j++ {
				rollPA := kmem.WalkUserPageTableWithL0(
					callerVA+uintptr(j)*kmem.PageSize, callerShepherd.PageTableL0PA)
				rollPA = rollPA &^ (kmem.PageSize - 1)
				if d := kmem.GetPageDescriptor(rollPA); d != nil {
					d.RefCount--
					if d.RefCount <= 1 {
						d.Flags &^= kmem.PD_SHARED
					}
				}
			}
			return -12 // ENOMEM
		}
	}

	// Add single span covering all target pages.
	targetShepherd.Spans.Add(targetVABase, totalSize)

	return int64(targetVABase)
}
