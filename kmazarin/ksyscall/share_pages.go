package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// MaxTransferPages is the maximum number of pages that can be transferred in a single call.
// PAs are stored in a stack array (16KB at 2048 entries). This function is not nosplit,
// so Go will grow the goroutine stack as needed.
const MaxTransferPages = 2048

// SyscallTransferPages transfers ownership of contiguous pages from the calling
// shepherd to a target shepherd. The pages are unmapped from the caller's address space,
// their ownership is updated, and they are mapped into the target's address space.
//
// Args:
//
//	arg0 = targetPID (shepherd to transfer pages to)
//	arg1 = sourceVA  (start of contiguous page range in caller's address space)
//	arg2 = numPages  (number of 4KB pages to transfer, 1..256)
//	arg3 = elfFlags  (ELF permission flags for target mapping; 0 = RW)
//
// Returns: target VA base on success, or negative errno on failure.
func SyscallTransferPages(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	targetPID := int16(arg0)
	sourceVA := uintptr(arg1)
	numPages := int(arg2)
	elfFlags := uint32(arg3)

	// Validate numPages
	if numPages < 1 || numPages > MaxTransferPages {
		return -22 // EINVAL
	}

	// Validate sourceVA is page-aligned
	if sourceVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	// Get caller shepherd
	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM — kernel context
	}
	callerSID := int16(callerShepherd.PID)

	// Can't transfer to self
	if targetPID == callerSID {
		return -22 // EINVAL
	}

	// Look up target shepherd
	targetShepherd := proc.FindShepherdBySID(proc.ShepherdId(targetPID))
	if targetShepherd == nil {
		return -3 // ESRCH — no such process
	}
	if targetShepherd.PageTableL0PA == 0 {
		return -3 // ESRCH — target has no address space
	}

	sourceL0PA := callerShepherd.PageTableL0PA

	// Disable IRQs across both passes to prevent async preemption between
	// page validation (Pass 1) and ownership transfer (Pass 2). Without this,
	// a timer IRQ could trigger goroutine preemption, allowing another goroutine
	// to call exit_group — CleanupShepherdPages would free pages that Pass 1
	// already validated, causing use-after-free in Pass 2.
	savedDAIF := saveAndDisableIRQs()

	// Pass 1: Validate all pages exist and are owned by caller.
	// Store resolved PAs in stack array.
	var pas [MaxTransferPages]uintptr
	for i := 0; i < numPages; i++ {
		va := sourceVA + uintptr(i)*kmem.PageSize
		pa := kmem.DemandMapUserPage(va, sourceL0PA)
		if pa == 0 {
			restoreIRQs(savedDAIF)
			serial.RawUARTPuts("[IPC] TransferPages: page not mapped at VA 0x")
			serial.RawUARTHex64(uint64(va))
			serial.RawUARTPuts("\r\n")
			return -14 // EFAULT
		}
		// Strip page offset (WalkUserPageTableWithL0 may include offset bits)
		pa = pa &^ (kmem.PageSize - 1)
		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != callerSID {
			restoreIRQs(savedDAIF)
			serial.RawUARTPuts("[IPC] TransferPages: page not owned by caller at PA 0x")
			serial.RawUARTHex64(uint64(pa))
			serial.RawUARTPuts("\r\n")
			return -1 // EPERM
		}
		pas[i] = pa
	}

	// Allocate target VA range
	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForShepherd(targetShepherd, totalSize)
	if targetVABase == 0 {
		restoreIRQs(savedDAIF)
		return -12 // ENOMEM
	}

	// Add span to target shepherd
	targetShepherd.Spans.Add(targetVABase, totalSize)

	// Pass 2: Transfer — unmap from source, change ownership, map into target
	for i := 0; i < numPages; i++ {
		va := sourceVA + uintptr(i)*kmem.PageSize
		pa := pas[i]

		// Unmap from caller
		kmem.UnmapUserPageWithL0(va, sourceL0PA)

		// Transfer ownership
		kmem.TransferPageOwnership(pa, callerSID, targetPID)

		// Map into target
		targetVA := uintptr(targetVABase) + uintptr(i)*kmem.PageSize
		kmem.MapPageInProcess(targetPID, targetVA, pa, elfFlags)
	}

	// Remove source span
	callerShepherd.Spans.Remove(uint64(sourceVA), totalSize)

	restoreIRQs(savedDAIF)

	return int64(targetVABase)
}
