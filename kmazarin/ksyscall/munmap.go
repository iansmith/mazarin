package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
)

// SyscallMunmap implements the munmap(2) syscall.
// Unmaps pages in the specified range, updates span tracking,
// and returns physical frames to the buddy allocator via PageDescriptor refcounting.
//
// If the range overlaps a MAZARIN_CONTIGUOUS DMA clump, the clump is handled
// specially: if no I/O is in flight, the entire contiguous block is freed back
// to the buddy allocator as a single unit. If I/O is in flight, PendingRelease
// is set and page release is deferred to the completion handler.
//
//go:nosplit
func SyscallMunmap(addr, length, _, _, _, _ uint64) int64 {
	if length == 0 {
		return 0 // Nothing to unmap
	}

	// Align addr down and length up to page boundaries
	pageSize := uint64(4096)
	alignedAddr := addr &^ (pageSize - 1)
	alignedEnd := (addr + length + pageSize - 1) &^ (pageSize - 1)
	alignedLength := alignedEnd - alignedAddr

	// Check if this overlaps a DMA clump (MAZARIN_CONTIGUOUS pages)
	p := proc.CurrentShepherd()
	if p != nil {
		clumpIdx := findClumpOverlap(p, uintptr(alignedAddr), uintptr(alignedEnd))
		if clumpIdx >= 0 {
			munmapClump(p, clumpIdx)
			// Still remove the span and return — the pages are handled by munmapClump
			removeSpan(alignedAddr, alignedLength)
			return 0
		}
	}

	// For file-backed mappings: flush pages and clean up handler-side
	// mappings via IPC rounds. Must happen before RemoveFileMapping
	// clears metadata and before freeing physical pages.
	//
	// IMPORTANT: flushAndCleanupPages uses SetSyscallSwitchTarget which
	// does NOT stop execution — code after it continues running before
	// the IPC round-trip completes. The handler still needs to read the
	// page data through its VA, so we must NOT free the physical pages
	// here. They are released by handleFlushReply after the handler has
	// finished reading and the handler PTEs are unmapped.
	//
	// Pass the file-offset range so a partial munmap doesn't drop pages
	// outside [alignedAddr, alignedEnd). Without the range, the handler
	// would flush every cached page for fm.FD's inode, the kernel would
	// release them all, and the un-unmapped portion of mail's mapping
	// would be left with live PTEs to recycled physical pages.
	fileBacked := false
	if p != nil {
		fm := p.FindFileMappingByVA(alignedAddr)
		if fm != nil {
			fileBacked = true
			startOff := fm.FileOffset + (alignedAddr - fm.StartVA)
			if alignedLength != fm.Length {
				klog.Errf("[munmap:PARTIAL] sid=%d fd=%d fmVA=%x fmLen=%x unmapVA=%x unmapLen=%x\n",
					p.PID, fm.FD, fm.StartVA, fm.Length, alignedAddr, alignedLength)
			}
			flushAndCleanupPages(uint64(fm.FD), int16(p.PID), startOff, alignedLength)
		}
	}

	// Remove file-backed mapping metadata for this VA range (if any)
	if p != nil {
		p.RemoveFileMapping(alignedAddr, alignedLength)
	}

	// Unmap caller PTEs and (for non-file-backed pages) free physical pages.
	removeSpan(alignedAddr, alignedLength)

	// Bug B family instrumentation: log when an IPC-region (>= ipcDataVAStart)
	// page that was ever shared (PD_SHARED bit set) returns to the buddy
	// via this path. The PD_SHARED filter is what makes this useful — it
	// excludes the volumes of sole-owned IPC-region churn from rachel/linux
	// runtime mmap/munmap that would otherwise drown the signal.
	// preRefCount=1 + PD_SHARED set means "last mapping of a shared page
	// freed", which is the suspect case for stale-PTE corruption.
	for va := alignedAddr; va < alignedEnd; va += pageSize {
		pa := kmem.UnmapUserPage(uintptr(va))
		if pa != 0 && !fileBacked {
			kmem.ReleasePageByPA(pa)
		}
	}

	return 0
}

// findClumpOverlap returns the index of the first DMA clump that overlaps
// [rangeStart, rangeEnd), or -1 if none.
//
//go:nosplit
func findClumpOverlap(p *proc.Shepherd, rangeStart, rangeEnd uintptr) int32 {
	for i := int32(0); i < p.NumDMAClumps; i++ {
		c := &p.DMAClumps[i]
		clumpEnd := c.StartVA + uintptr(c.NumPages)*4096
		// Overlap if: rangeStart < clumpEnd && rangeEnd > c.StartVA
		if rangeStart < clumpEnd && rangeEnd > c.StartVA {
			return i
		}
	}
	return -1
}

// munmapClump handles munmap of a MAZARIN_CONTIGUOUS DMA clump.
// Unmaps all PTEs for the clump's pages. If no I/O is in flight, frees the
// contiguous block immediately. Otherwise sets PendingRelease for deferred
// cleanup by the completion handler.
//
//go:nosplit
func munmapClump(p *proc.Shepherd, idx int32) {
	c := &p.DMAClumps[idx]

	// Unmap all PTEs so userspace can no longer access the pages
	buddyPages := 1 << uint(c.BuddyOrder)
	for i := 0; i < buddyPages; i++ {
		va := c.StartVA + uintptr(i)*4096
		kmem.UnmapUserPage(va)
		// Don't call ReleasePageByPA — pages are freed as a contiguous block
	}

	inflight := atomic.LoadInt32(&c.InFlight)
	if inflight == 0 {
		// [MAZ-179 tier-12 probe] witness DMA references before the free
		maz179DmaFreeProbe(p, c)
		// Safe to free immediately
		kmem.BuddyFreeTyped(c.StartPA, c.BuddyOrder, kmem.PageUserDMA)
		p.LockClumps()
		p.RemoveClump(idx)
		p.UnlockClumps()
	} else {
		// I/O in flight — defer release to completion handler
		c.PendingRelease = true
	}
}

// CleanupShepherdDMAClumps releases all DMA clumps for a dying shepherd.
// Called from shepherd cleanup before pages are freed.
// Clumps with InFlight > 0 are marked ShepherdDead; the completion handler
// will free them when the last in-flight I/O completes.
func CleanupShepherdDMAClumps(p *proc.Shepherd) {
	// Walk backwards so RemoveClump's swap doesn't skip entries
	for i := p.NumDMAClumps - 1; i >= 0; i-- {
		c := &p.DMAClumps[i]
		inflight := atomic.LoadInt32(&c.InFlight)
		if inflight == 0 {
			// [MAZ-179 tier-12 probe] witness DMA references before the free
			maz179DmaFreeProbe(p, c)
			kmem.BuddyFreeTyped(c.StartPA, c.BuddyOrder, kmem.PageUserDMA)
			p.LockClumps()
			p.RemoveClump(i)
			p.UnlockClumps()
		} else {
			c.ShepherdDead = true
		}
	}
}
