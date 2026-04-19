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

