package ksyscall

// [MAZ-179 probe — NOT FOR MERGE, tier 12] DMA-descriptor liveness probe at
// the two DMA-clump free sites (munmapClump, CleanupShepherdDMAClumps).
// Detection only — the free still proceeds; the probe records whether any
// device-visible reference into the freed range was live at that instant.
// Counters ride the [MAZ179HB] heartbeat line. See proc/maz179_counters.go
// (tier-12 block) and kmazarin/maz179_dma_scan.go for the scan itself.

import (
	"sync/atomic"
	_ "unsafe" // go:linkname

	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

//go:linkname dmaScanRange main.DMAScanRange
func dmaScanRange(startPA, endPA uintptr) (blkHits, netHits int32)

// maz179DmaFreeProbe runs immediately before BuddyFreeTyped on a clump with
// InFlight==0. Nosplit: munmapClump is nosplit. No klog — counters only.
//
//go:nosplit
func maz179DmaFreeProbe(p *proc.Shepherd, c *proc.DMAClump) {
	atomic.AddUint32(&proc.DmaClumpFrees, 1)
	atomic.AddUint32(&proc.DmaClumpFreePages, uint32(c.NumPages))
	if c.NumPages >= 64 {
		// Net's DMA pool is the only >=64-page clump in the system
		// (128 pages; block scratch clumps are 1-16 pages).
		atomic.AddUint32(&proc.NetPoolFrees, 1)
	}
	// Suspect-#1 witness: InFlight==0 (that's why we're freeing) while some
	// submit is between UnlockClumps and Notify. Over-approximate (any clump,
	// any owner) — corroborated by the precise slot scan below.
	if atomic.LoadInt32(&proc.BlkSubmitWindow) != 0 {
		atomic.AddUint32(&proc.BlkFreeRace, 1)
	}
	endPA := c.StartPA + uintptr(c.NumPages)*kmem.PageSize
	blkHits, netHits := dmaScanRange(c.StartPA, endPA)
	if blkHits != 0 {
		atomic.AddUint32(&proc.DmaFreeBlkHits, uint32(blkHits))
	}
	if netHits != 0 {
		atomic.AddUint32(&proc.DmaFreeNetHits, uint32(netHits))
	}
	if (blkHits != 0 || netHits != 0) &&
		atomic.CompareAndSwapUint64(&proc.DmaFreeFirstPA, 0, uint64(c.StartPA)) {
		atomic.StoreInt32(&proc.DmaFreeFirstSID, int32(p.PID))
	}
}
