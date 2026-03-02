package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// SyscallMapSharedPage creates a shared mapping of a page owned by another priest
// into the calling priest's address space. The page's refcount is incremented and
// PD_SHARED flag is set. Both priests can then access the same physical page.
//
// Args:
//
//	arg0 = ownerPID  (priest that owns the page)
//	arg1 = ownerVA   (VA of the page in the owner's address space)
//	arg2 = elfFlags  (ELF permission flags for caller's mapping; 0 = RW)
//
// Returns: caller VA on success, or negative errno on failure.
func SyscallMapSharedPage(arg0, arg1, arg2, _, _, _ uint64) int64 {
	ownerPID := int16(arg0)
	ownerVA := uintptr(arg1)
	elfFlags := uint32(arg2)

	// Validate ownerVA is page-aligned
	if ownerVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	// Get caller priest
	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM — kernel context
	}
	callerPID := int16(callerPriest.PID)

	// Look up owner priest
	ownerPriest := proc.FindPriestByPID(proc.PriestId(ownerPID))
	if ownerPriest == nil {
		return -3 // ESRCH — no such process
	}
	if ownerPriest.PageTableL0PA == 0 {
		return -3 // ESRCH — owner has no address space
	}

	// Resolve PA from owner's page table
	pa := kmem.WalkUserPageTableWithL0(ownerVA, ownerPriest.PageTableL0PA)
	if pa == 0 {
		serial.RawUARTPuts("[IPC] MapSharedPage: page not mapped in owner at VA 0x")
		serial.RawUARTHex64(uint64(ownerVA))
		serial.RawUARTPuts("\r\n")
		return -14 // EFAULT
	}
	// Strip page offset
	pa = pa &^ (kmem.PageSize - 1)

	// Verify ownership
	desc := kmem.GetPageDescriptor(pa)
	if desc == nil || desc.Owner != ownerPID {
		serial.RawUARTPuts("[IPC] MapSharedPage: page not owned by specified priest at PA 0x")
		serial.RawUARTHex64(uint64(pa))
		serial.RawUARTPuts("\r\n")
		return -1 // EPERM
	}

	// Increment refcount and mark as shared
	desc.RefCount++
	desc.Flags |= kmem.PD_SHARED

	// Allocate caller VA
	callerVA := bumpAllocForPriest(callerPriest, uint64(kmem.PageSize))
	if callerVA == 0 {
		// Roll back refcount
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	// Add span to caller
	callerPriest.Spans.Add(callerVA, uint64(kmem.PageSize))

	// Map into caller's address space
	if !kmem.MapPageInProcess(callerPID, uintptr(callerVA), pa, elfFlags) {
		// Roll back
		callerPriest.Spans.Remove(callerVA, uint64(kmem.PageSize))
		desc.RefCount--
		if desc.RefCount <= 1 {
			desc.Flags &^= kmem.PD_SHARED
		}
		return -12 // ENOMEM
	}

	return int64(callerVA)
}
