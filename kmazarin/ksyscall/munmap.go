
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
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

	// Normal path: unmap individual pages
	removeSpan(alignedAddr, alignedLength)

	for va := alignedAddr; va < alignedEnd; va += pageSize {
		pa := kmem.UnmapUserPage(uintptr(va))
		if pa != 0 {
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
		// Safe to free immediately
		serial.RawUARTPuts("[munmap] freeing clump VA=")
		serial.RawUARTHex64(uint64(c.StartVA))
		serial.RawUARTPuts(" order=")
		serial.RawUARTHex64(uint64(c.BuddyOrder))
		serial.RawUARTPuts("\r\n")
		kmem.BuddyFreeTyped(c.StartPA, c.BuddyOrder, kmem.PageUserDMA)
		p.RemoveClump(idx)
	} else {
		// I/O in flight — defer release to completion handler
		serial.RawUARTPuts("[munmap] deferring clump release (inflight=")
		serial.RawUARTHex64(uint64(inflight))
		serial.RawUARTPuts(")\r\n")
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
			kmem.BuddyFreeTyped(c.StartPA, c.BuddyOrder, kmem.PageUserDMA)
			p.RemoveClump(i)
		} else {
			c.ShepherdDead = true
		}
	}
}
