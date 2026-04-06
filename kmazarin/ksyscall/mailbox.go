package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// SyscallSharePages maps a page from the caller's address space into
// a target shepherd's address space and caches the VA↔VA translation.
// arg0 = targetSID (shepherd to map page into)
// arg1 = callerVA  (VA of the page/ring in caller's space — need not be page-aligned)
// Returns: target VA (with offset preserved) on success, or negative errno.
//
//go:noinline
func SyscallSharePages(arg0, arg1, _, _, _, _ uint64) int64 {
	targetSID := int16(arg0)
	callerVA := uintptr(arg1)

	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	callerSID := int16(callerShepherd.PID)

	if callerSID == targetSID {
		return -22 // EINVAL — can't map into self
	}

	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetSID))
	if targetShepherd == nil || targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	pageOffset := callerVA & (kmem.PageSize - 1)
	callerPageVA := callerVA &^ (kmem.PageSize - 1)

	// Check if already cached
	if targetPageVA, ok := lookupVACache(callerSID, targetSID, callerPageVA); ok {
		return int64(targetPageVA + pageOffset)
	}

	// Resolve PA from caller's page table
	pa := kmem.WalkUserPageTableWithL0(callerPageVA, callerShepherd.PageTableL0PA)
	if pa == 0 {
		pa = kmem.DemandMapUserPage(callerPageVA, callerShepherd.PageTableL0PA)
		if pa == 0 {
			klog.Errf("[SharePages] page not mapped in caller\n")
			return -14 // EFAULT
		}
	}
	pa = pa &^ (kmem.PageSize - 1)

	// Verify ownership
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != callerSID {
		klog.Errf("[SharePages] page not owned by caller\n")
		return -1 // EPERM
	}

	// Increment refcount, mark shared
	desc.RefCount++
	desc.Flags |= kmem.PD_SHARED

	// Allocate VA in target
	targetPageVAu64 := bumpAllocForShepherd(targetShepherd, uint64(kmem.PageSize))
	if targetPageVAu64 == 0 {
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}
	targetPageVA := uintptr(targetPageVAu64)

	targetShepherd.Spans.Add(targetPageVAu64, uint64(kmem.PageSize))

	if !kmem.MapPageInProcess(targetSID, targetPageVA, pa, 0) {
		targetShepherd.Spans.Remove(targetPageVAu64, uint64(kmem.PageSize))
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	// Cache the translation
	addVACacheEntry(callerSID, targetSID, callerPageVA, targetPageVA)

	return int64(targetPageVA + pageOffset)
}
