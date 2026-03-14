// page_scanner.go - A/D bit scanning for page replacement framework (Stage 5)
//
// ScanAccessedBits walks all mapped user pages for a priest, clears the
// hardware Accessed bit on each, and tracks dirty state in the PageDescriptor.
// This implements the "clock" algorithm's reference check — the foundation
// for future LRU/clock page replacement.
//
// FindEvictionCandidates returns physical addresses of pages suitable for
// swap-out, ordered by eviction preference:
//   1. Clean + unaccessed (not recently used, no write-back needed)
//   2. Clean + accessed   (recently used, but clean — cheap to evict)
//   3. Dirty + unaccessed (not recently used, but needs write-back)
//
// Called from the timer bottom-half goroutine every ~1000 ticks.
//
// ARCHITECTURE NOTE: platformClearAccessed/platformClearDirty walk the page
// table via the platform's cached root pointer (ttbr0L0PA on ARM64). For
// priests whose page table is not currently active in the MMU, these calls
// return false (no-op). The PA lookup via WalkUserPageTableWithL0 is always
// correct (takes explicit l0PA). A future stage will add arch-neutral
// per-priest PTE write support to make the A/D clear work for all priests.
//
// TODO(Stage 5 deferred): ARM64 software dirty tracking. Cortex-A72 lacks
// FEAT_HAFDBS, so platformClearDirty is a no-op. The "AP bit permission trick"
// would map writable pages as read-only, take a permission fault on first
// write, and set a software dirty bit. This only matters when swap is
// implemented (dirty pages must be written back before eviction). Since swap
// I/O is stubs-only, this is deferred. See also paging_arm64.go:platformClearDirty.
//
// Design reference: design/MEMORY_OVERHAUL.md Stage 5

package kmem

import (
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

// A/D scan counters — updated by ScanAccessedBits, read by [E] event dump.
// Use atomic access: scanner runs from idle loop, dump runs from timer IRQ.
var scanRunCount uint64
var scanTotalAccessed uint64
var scanTotalPages uint64

// Previous snapshot for delta-since-last-dump reporting.
var scanPrevRunCount uint64
var scanPrevTotalAccessed uint64
var scanPrevTotalPages uint64

// ReadAndResetScanDeltas returns delta values since last call and resets the
// snapshot. Called from the [E] event dump in timer IRQ context.
//
//go:nosplit
func ReadAndResetScanDeltas() (runs, accessed, total uint64) {
	curRuns := atomic.LoadUint64(&scanRunCount)
	curAccessed := atomic.LoadUint64(&scanTotalAccessed)
	curTotal := atomic.LoadUint64(&scanTotalPages)
	runs = curRuns - scanPrevRunCount
	accessed = curAccessed - scanPrevTotalAccessed
	total = curTotal - scanPrevTotalPages
	scanPrevRunCount = curRuns
	scanPrevTotalAccessed = curAccessed
	scanPrevTotalPages = curTotal
	return
}

// ScanAccessedBits walks all mapped pages for the given priest, clears the
// Accessed bit on each, and returns (accessedCount, totalCount).
// Also propagates the hardware Dirty bit into the PageDescriptor's PD_DIRTY flag.
//
// Called from the timer bottom-half goroutine (KernelIdleLoop) every ~1000
// timer ticks. NOT nosplit — uses spans.ForEach and page table walking.
func ScanAccessedBits(priestID proc.PriestId) (accessedCount int, totalCount int) {
	if priestID < 0 || int(priestID) >= proc.MaxPriests {
		return
	}
	if !proc.PriestListInUse[priestID] {
		return
	}
	defer func() {
		atomic.AddUint64(&scanRunCount, 1)
		atomic.AddUint64(&scanTotalAccessed, uint64(accessedCount))
		atomic.AddUint64(&scanTotalPages, uint64(totalCount))
	}()

	p := &proc.PriestListData[priestID]
	l0PA := p.PageTableL0PA
	if l0PA == 0 {
		return // No page table allocated (priest never ran)
	}

	p.Spans.ForEach(func(start, length uint64) {
		end := start + length
		for va := uintptr(start); va < uintptr(end); va += PageSize {
			// Look up PA using the priest's page table (explicit l0PA).
			// This is correct regardless of which page table is active in the MMU.
			pa := WalkUserPageTableWithL0(va, l0PA)
			if pa == 0 {
				continue // Not mapped yet (demand paging)
			}
			totalCount++

			// Get descriptor; skip pinned pages (kernel pages, PT pages, MMIO).
			desc := GetPageDescriptor(pa)
			if desc != nil && desc.Flags&PD_PINNED != 0 {
				continue
			}

			// Clear the Accessed bit and record if it was set.
			// NOTE: On ARM64, this walks ttbr0L0PA (the platform's cached root).
			// If the priest's page table is not currently active, platformClearAccessed
			// returns false (no match found) — acceptable for the stub framework.
			wasAccessed := platformClearAccessed(va)
			if wasAccessed {
				accessedCount++
			}

			// Clear the hardware Dirty bit and propagate to PageDescriptor.
			// On ARM64 without FEAT_HAFDBS, platformClearDirty is a no-op (returns false).
			wasDirty := platformClearDirty(va)
			if wasDirty && desc != nil {
				desc.Flags |= PD_DIRTY
			}
		}
	})

	return
}

// FindEvictionCandidates returns up to count physical addresses suitable for
// eviction from the given priest's address space. Pages are ranked:
//   1. Clean + unaccessed (best: not recently used, no write-back needed)
//   2. Clean + accessed   (recently used but clean — cheap to evict)
//   3. Dirty + unaccessed (not recently used, but requires write-back)
//
// Pinned pages and already-swapped pages are never returned.
// Returned PAs are still mapped; the caller must call SwapOutPage to evict.
func FindEvictionCandidates(priestID proc.PriestId, count int) []uintptr {
	if priestID < 0 || int(priestID) >= proc.MaxPriests || count <= 0 {
		return nil
	}
	if !proc.PriestListInUse[priestID] {
		return nil
	}

	p := &proc.PriestListData[priestID]
	l0PA := p.PageTableL0PA
	if l0PA == 0 {
		return nil
	}

	// Three fixed-size buckets for prioritized selection.
	// Fixed arrays avoid Go map/dynamic-alloc in the kmem package.
	const maxPerBucket = 64
	var cleanUnaccessed [maxPerBucket]uintptr
	var cleanAccessed [maxPerBucket]uintptr
	var dirtyUnaccessed [maxPerBucket]uintptr
	var nCleanUnacc, nCleanAcc, nDirtyUnacc int

	p.Spans.ForEach(func(start, length uint64) {
		end := start + length
		for va := uintptr(start); va < uintptr(end); va += PageSize {
			if nCleanUnacc+nCleanAcc+nDirtyUnacc >= maxPerBucket {
				return // Buckets full
			}

			pa := WalkUserPageTableWithL0(va, l0PA)
			if pa == 0 {
				continue
			}

			desc := GetPageDescriptor(pa)
			if desc == nil {
				continue
			}
			if desc.Flags&PD_PINNED != 0 || desc.Flags&PD_SWAPPED != 0 {
				continue
			}

			// Read current PTE flags to check hardware A/D bits.
			pte, _, ok := platformReadPTEAt(va)
			if !ok {
				// PTE not found via current page table root — use descriptor flags only.
				// This happens when the priest's page table is not currently active.
				// Treat as unaccessed+clean (conservative — may over-evict).
				if nCleanUnacc < maxPerBucket {
					cleanUnaccessed[nCleanUnacc] = pa
					nCleanUnacc++
				}
				continue
			}

			flags := platformPTEToFlags(pte)
			accessed := flags.Has(PF_Accessed)
			dirty := flags.Has(PF_Dirty) || desc.Flags&PD_DIRTY != 0

			switch {
			case !dirty && !accessed:
				if nCleanUnacc < maxPerBucket {
					cleanUnaccessed[nCleanUnacc] = pa
					nCleanUnacc++
				}
			case !dirty && accessed:
				if nCleanAcc < maxPerBucket {
					cleanAccessed[nCleanAcc] = pa
					nCleanAcc++
				}
			case dirty && !accessed:
				if nDirtyUnacc < maxPerBucket {
					dirtyUnaccessed[nDirtyUnacc] = pa
					nDirtyUnacc++
				}
			// dirty+accessed: worst candidate, skip
			}
		}
	})

	// Fill result in eviction preference order.
	result := make([]uintptr, 0, count)
	for i := 0; i < nCleanUnacc && len(result) < count; i++ {
		result = append(result, cleanUnaccessed[i])
	}
	for i := 0; i < nCleanAcc && len(result) < count; i++ {
		result = append(result, cleanAccessed[i])
	}
	for i := 0; i < nDirtyUnacc && len(result) < count; i++ {
		result = append(result, dirtyUnaccessed[i])
	}
	return result
}

// LogScanResult logs a one-line A/D scan summary for a priest.
// Uses serial directly to avoid fmt/console allocation in the kmem package.
func LogScanResult(priestID proc.PriestId, accessed, total int) {
	serial.RawUARTPuts("[scan] priest ")
	serial.RawUARTHex64(uint64(priestID))
	serial.RawUARTPuts(": accessed=")
	serial.RawUARTHex64(uint64(accessed))
	serial.RawUARTPuts("/")
	serial.RawUARTHex64(uint64(total))
	serial.RawUARTPuts("\r\n")
}
