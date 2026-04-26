package ksyscall

// mmap_writeback.go — Flush dirty mmap pages and clean up handler-side mappings.
//
// When a file-backed mapping is munmap'd (or the shepherd dies), the kernel
// sends MmapPageFlush IPC rounds to the linux shepherd. Each round:
//  1. Handler flushes dirty pages to ext2 (first round only)
//  2. Handler removes up to 511 page cache entries, writes VAs to response page
//  3. Kernel unmaps those handler-side PTEs
//  4. If count == 511, kernel sends another round
//
// This replaces both the old batch-copy writeback AND cacheRetainedPages.

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

const maxFlushVAsPerRound = 511

// flushAndCleanupPages sends the first MmapPageFlush IPC round to the linux
// shepherd. The reply path (handleFlushReply) processes the response and
// sends additional rounds if needed.
//
// fd=0xFFFFFFFF means flush all fds (death cleanup); in that case
// startOffset/length are ignored. For a normal munmap, startOffset+length
// constrain the flush to the file-offset range corresponding to the unmapped
// VA range, so partial munmap doesn't drop pages outside it. length=0 also
// means "all" (used internally for death cleanup).
func flushAndCleanupPages(fd uint64, callerSID int16, startOffset, length uint64) {
	handlerSID := int16(atomic.LoadInt32(&syscallDelegates[sysid.Write].pid))
	if handlerSID < 0 {
		klog.Errf("[mmap-flush] no Write delegate handler\n")
		return
	}
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
	if handlerShepherd == nil {
		klog.Errf("[mmap-flush] handler shepherd gone\n")
		return
	}

	_, callerTID := getCurrentThreadSIDAndTID()
	if int(callerTID) >= MaxDelegateThreads {
		klog.Errf("[mmap-flush] TID %d out of range\n", callerTID)
		return
	}

	// Allocate response page
	responsePA := kmem.AllocPage(kmem.PageSharedIPC, handlerSID)
	if responsePA == 0 {
		klog.Errf("[mmap-flush] ENOMEM response page\n")
		return
	}

	// Map response page into handler's address space
	responseHandlerVA := bumpAllocForShepherd(handlerShepherd, 4096)
	if responseHandlerVA == 0 {
		kmem.ReleasePageByPA(responsePA)
		klog.Errf("[mmap-flush] ENOMEM handler VA\n")
		return
	}
	kmem.MapPageInProcess(handlerSID, uintptr(responseHandlerVA), responsePA, 0) // RW
	handlerShepherd.Spans.Add(responseHandlerVA, 4096)


	// Set up DelegateCallInfo with flush round state
	info := &delegateCallInfos[callerTID]
	info.DataPagePA = 0
	info.DataPageVA = 0
	info.HandlerSID = handlerSID
	info.CallerSID = callerSID
	info.CallerBufVA = 0
	info.CallerBufLen = 0
	info.CallerL0PA = uintptr(kmem.ReadCurrentL0PA())
	info.SysID = sysid.MmapPageFlush
	info.WritableMapping = false
	info.InUse = true
	info.FlushResponsePA = responsePA
	info.FlushResponseVA = responseHandlerVA
	info.FlushFD = fd
	info.FlushCallerSID = callerSID
	info.FlushOffset = startOffset
	info.FlushLength = length

	sendFlushRound(info, handlerSID, callerSID, callerTID)
}

// sendFlushRound sends one MmapPageFlush IPC to the handler.
// Called from flushAndCleanupPages (first round) and handleFlushReply (subsequent rounds).
func sendFlushRound(info *DelegateCallInfo, handlerSID int16, callerSID int16, callerTID int16) {
	reqPayload := ipc.FSDelegateReqPayload{
		SysID:     uint16(sysid.MmapPageFlush),
		CallerSID: callerSID,
		CallerTID: callerTID,
		Args:      [6]uint64{info.FlushFD, uint64(callerSID), info.FlushOffset, info.FlushLength, 0, 0},
		DataVA:    info.FlushResponseVA,
		DataLen:   4096,
	}
	msg := ipc.EncodeFSDelegateReq(&reqPayload)
	handlerRingIdx := syscallDelegates[sysid.Write].ringIdx
	result, _ := uringSendKernel(-1, handlerSID, handlerRingIdx, uintptr(unsafe.Pointer(&msg)))
	if result < 0 {
		info.InUse = false
		cleanupFlushResponsePage(info, handlerSID)
		klog.Errf("[mmap-flush] uring send failed\n")
		return
	}

	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		klog.Errf("[mmap-flush] NO NEXT THREAD\n")
		return
	}
	SetSyscallSwitchTarget(ctx)
}

// handleFlushReply processes the response page from a MmapPageFlush reply.
// Unmaps handler VAs listed in the response. If count == 511, sends another
// round (keeping the caller blocked). Otherwise frees the response page.
//
// Called from SyscallReply in the handler's SVC context.
// Returns true if another round was sent (caller stays blocked).
func handleFlushReply(callerTID int16, info *DelegateCallInfo) bool {
	handlerSID := info.HandlerSID
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))

	// Read response page via kernel scratch mapping
	scratchVA := kmem.MapPAToKernelScratch(info.FlushResponsePA)
	if scratchVA == 0 {
		klog.Errf("[mmap-flush] can't map response page for read\n")
		cleanupFlushResponsePage(info, handlerSID)
		return false
	}

	count := *(*uint32)(unsafe.Pointer(scratchVA))
	vasPtr := (*[maxFlushVAsPerRound]uint64)(unsafe.Pointer(scratchVA + 8))


	// Unmap each handler VA from the handler's page table and release
	// the physical page. The caller's PTE was already removed by
	// SyscallMunmap, but ReleasePageByPA was deferred until now so the
	// handler could read the page data for the ext2 flush.
	if handlerShepherd != nil {
		for i := uint32(0); i < count && i < maxFlushVAsPerRound; i++ {
			hva := uintptr(vasPtr[i])
			if hva == 0 {
				continue
			}
			pa := kmem.UnmapUserPageWithL0(hva, handlerShepherd.PageTableL0PA)
			handlerShepherd.Spans.Remove(uint64(hva), 4096)
			if pa != 0 {
				// Handler PTE is now gone — safe to release. Clear FILE_BACKED so the
				// guard in releasePageByPA doesn't block this legitimate free.
				if desc := kmem.GetPageDescriptor(pa); desc != nil {
					desc.Flags &^= kmem.PD_FILE_BACKED
				}
				kmem.ReleasePageByPA(pa)
			}
		}
	}

	// If response was full, there may be more — send another round
	if count == maxFlushVAsPerRound {

		// Re-send flush IPC. The info fields are still set up correctly.
		// We need to send from the handler's SVC context. uringSendKernel
		// just enqueues a message — it doesn't block.
		reqPayload := ipc.FSDelegateReqPayload{
			SysID:     uint16(sysid.MmapPageFlush),
			CallerSID: info.FlushCallerSID,
			CallerTID: callerTID,
			Args:      [6]uint64{info.FlushFD, uint64(info.FlushCallerSID), info.FlushOffset, info.FlushLength, 0, 0},
			DataVA:    info.FlushResponseVA,
			DataLen:   4096,
		}
		msg := ipc.EncodeFSDelegateReq(&reqPayload)
		flushRingIdx := syscallDelegates[sysid.Write].ringIdx
		result, _ := uringSendKernel(-1, handlerSID, flushRingIdx, uintptr(unsafe.Pointer(&msg)))
		if result < 0 {
			klog.Errf("[mmap-flush] uring send failed on round continuation\n")
			cleanupFlushResponsePage(info, handlerSID)
			return false
		}
		// Keep caller blocked — don't wake it
		return true
	}

	// All done — clean up response page
	cleanupFlushResponsePage(info, handlerSID)
	return false
}

// cleanupFlushResponsePage unmaps and frees the response page.
func cleanupFlushResponsePage(info *DelegateCallInfo, handlerSID int16) {
	if info.FlushResponseVA != 0 {
		handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
		if handlerShepherd != nil {
			kmem.UnmapUserPageWithL0(uintptr(info.FlushResponseVA), handlerShepherd.PageTableL0PA)
			handlerShepherd.Spans.Remove(info.FlushResponseVA, 4096)
		}
	}
	if info.FlushResponsePA != 0 {
		kmem.ReleasePageByPA(info.FlushResponsePA)
	}
	info.FlushResponsePA = 0
	info.FlushResponseVA = 0
}

// WriteBackSharedMmapOnDeath flushes and cleans up all file-backed mmap pages
// for a dying shepherd. Called from TerminateShepherd Phase 1 (pre-lock).
func WriteBackSharedMmapOnDeath(pid int16) {
	for i := int16(0); i < int16(len(proc.ShepherdListData)); i++ {
		if !proc.ShepherdListInUse[i] {
			continue
		}
		if int16(proc.ShepherdListData[i].PID) != pid {
			continue
		}
		p := &proc.ShepherdListData[i]

		// Check if any file mappings exist
		hasFileMappings := false
		for j := 0; j < proc.MaxFileMappings; j++ {
			if p.FileMappings[j].InUse {
				hasFileMappings = true
				break
			}
		}

		if hasFileMappings {
			// Flush all fds + clean up all handler-side mappings in rounds.
			// length=0 + fd=0xFFFFFFFF → drain everything for this sid.
			flushAndCleanupPages(0xFFFFFFFF, pid, 0, 0)
		}
		return
	}
}
