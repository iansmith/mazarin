// page_tracker.go - Tracks metadata about allocated pages
//
// PageAllocInfo records are maintained in a static array. The top-half
// (page fault handler, nosplit) queues deferred records; the bottom-half
// goroutine drains the queue and inserts them here.
//
// This provides visibility into what every allocated page is used for,
// which process owns it, and what order it was allocated at.

package kmem

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// PageAllocType identifies the purpose of a page allocation.
type PageAllocType uint8

const (
	PageAllocKernelHeap PageAllocType = iota
	PageAllocKernelPT
	PageAllocUser
	PageAllocUserPT
	PageAllocFileBuffer
)

// PageAllocInfo records metadata about a single page allocation.
type PageAllocInfo struct {
	PA         uintptr       // Physical address of the page
	VA         uintptr       // Virtual address (0 if not mapped to user VA)
	Type       PageAllocType // Purpose of the allocation
	ShepherdID int16         // Process ID (-1 = kernel)
	ThreadID   int16         // Thread that allocated
	Order      uint8         // Buddy order (0 = single page)
}

// MaxTrackedPages is the capacity of the page tracker array.
// 32K entries * 24 bytes = ~768KB. Covers up to 128MB of 4KB pages.
const MaxTrackedPages = 32768

var (
	pageTracker  [MaxTrackedPages]PageAllocInfo
	trackerCount int
	trackerLock  Spinlock

	// trackerFullWarnings counts how many times TrackPage has hit the
	// tracker-full branch below. Read via GetTrackerFullWarnings(); used by
	// the page_tracker_selftest (MAZ-163) to assert saturation produces zero
	// warnings. Accessed with atomics since GetTrackerFullWarnings() reads
	// it without taking trackerLock.
	trackerFullWarnings uint64
)

// --- PA -> tracker-slot index (MAZ-163) ---
//
// A flat array indexed by PFN — (PA - trackerIdxPoolStart) >> PageShift —
// storing the tracker slot number for that PA (0 = untracked, else slot+1).
// This mirrors page_descriptor.go's pdAt/pdBase exactly (same bump
// allocator, same PFN scheme), per that file's package-wide rule: "No Go
// maps. No Go slices. All data structures are flat arrays accessed via raw
// pointer arithmetic." UntrackPage is not itself nosplit — it only runs from
// the bottom-half drain — but it shares the package's flat-array convention
// rather than introducing the one Go-map PA index in kmem.
var (
	trackerIdxBase        uintptr // VA of the PFN -> (slot+1) index array
	trackerIdxPoolStart   uintptr // Start of pool (PA), mirrors pdPoolStart
	trackerIdxCapacity    uint64  // Number of PFN entries covered
	trackerIdxInitialized uint32  // Atomic: 1 = ready
)

// trackerIdxAt returns a pointer to the uint32 slot-plus-one for PFN index idx.
// No bounds checking — caller must verify idx < trackerIdxCapacity.
//
//go:nosplit
func trackerIdxAt(idx uintptr) *uint32 {
	return (*uint32)(unsafe.Pointer(trackerIdxBase + idx*4))
}

// InitPageTrackerIndex allocates the PFN->slot index array. Called once,
// from InitUnifiedPool right after InitPageDescriptors (same poolStart/
// poolEnd, same bump allocator, so its pages are counted as bootstrap pages
// the same way).
//
// COST: one uint32 per POOL page — sized by the pool, not by how many
// records the tracker actually holds. For the ARM64 2.5 GiB unified pool
// (0x5BC58000-0xFBC58000, 655,360 pages) that is 2.5 MiB / 640 pages,
// bump-allocated at boot and never freed. For scale, the PageDescriptor
// array on the line above is 16 bytes per pool page = 10 MiB; the two
// together are ~0.5% of the pool. Direct PFN indexing means a slot for
// every possible address rather than every occupied one — that is the
// price of O(1) lookup with no hashing and no GC write barriers, which
// page_descriptor.go's "No Go maps. No Go slices." rule requires.
//
// The entry type is uint32 DELIBERATELY, not uint16. Slot+1 currently fits
// in a uint16 (MaxTrackedPages = 32768), so uint16 would halve this array —
// that trade was considered and declined, to keep the entry type
// independent of MaxTrackedPages. Do not "optimize" it without raising the
// coupling that would introduce.
func InitPageTrackerIndex(poolStart, poolEnd uintptr) {
	if atomic.LoadUint32(&trackerIdxInitialized) != 0 {
		return
	}

	trackerIdxPoolStart = poolStart
	totalPages := uint64(poolEnd-poolStart) / PageSize
	trackerIdxCapacity = totalPages

	arraySize := uintptr(totalPages) * 4 // one uint32 per PFN
	arrayPages := (arraySize + PageSize - 1) / PageSize

	globalPool.lock.Lock()
	allocSize := arrayPages * PageSize
	if globalPool.next+allocSize > globalPool.end {
		globalPool.lock.Unlock()
		serial.RawUARTPuts("[kmem] FATAL: pool too small for page tracker index array!\r\n")
		for {
		}
	}
	arrayPA := globalPool.next
	globalPool.next += allocSize
	globalPool.lock.Unlock()

	arrayVA := paToVA(arrayPA)
	for i := uintptr(0); i < arrayPages; i++ {
		Bzero4K(arrayVA + i*PageSize)
	}

	trackerIdxBase = arrayVA
	atomic.StoreUint32(&trackerIdxInitialized, 1)
}

// trackerIdxSlotPtr returns a pointer to the index slot for pa, or nil if
// the index isn't initialized yet or pa is outside the pool it covers.
func trackerIdxSlotPtr(pa uintptr) *uint32 {
	if atomic.LoadUint32(&trackerIdxInitialized) == 0 {
		return nil
	}
	if pa < trackerIdxPoolStart {
		return nil
	}
	idx := (pa - trackerIdxPoolStart) >> PageShift
	if idx >= uintptr(trackerIdxCapacity) {
		return nil
	}
	return trackerIdxAt(idx)
}

// TrackPage records a page allocation. Called from the bottom-half only
// (normal Go context, not nosplit).
func TrackPage(info PageAllocInfo) {
	trackerLock.Lock()
	if trackerCount < MaxTrackedPages {
		pageTracker[trackerCount] = info
		if slot := trackerIdxSlotPtr(info.PA); slot != nil {
			*slot = uint32(trackerCount) + 1 // +1: 0 means "untracked"
		}
		trackerCount++
		trackerLock.Unlock()
		return
	}
	trackerLock.Unlock()

	// Latch: log the "tracker full" warning once (on the first overflow),
	// not on every subsequent drop. Before this fix, a saturated tracker
	// meant a klog.Errf on EVERY allocation from then on — under sustained
	// allocation pressure (e.g. the MAZ-163 page_tracker_selftest cycle B),
	// that live-locks the boot by pegging the CPU writing serial output.
	// GetTrackerFullWarnings() is still incremented every time so callers
	// (the selftest, a future diagnostic) can observe how many records were
	// actually dropped, even though only the first drop gets logged.
	if atomic.AddUint64(&trackerFullWarnings, 1) == 1 {
		klog.Errf("[kmem] WARN: page tracker full (%d entries)\n", MaxTrackedPages)
	}
}

// TrackedPageCount returns the number of live tracker records. Called from
// the bottom-half / normal Go context (e.g. the page_tracker_selftest,
// MAZ-163) to observe the tracker shedding records on free.
func TrackedPageCount() int {
	trackerLock.Lock()
	n := trackerCount
	trackerLock.Unlock()
	return n
}

// GetTrackerFullWarnings returns the cumulative count of "page tracker full"
// warnings emitted by TrackPage since boot.
func GetTrackerFullWarnings() uint64 {
	return atomic.LoadUint64(&trackerFullWarnings)
}

// UntrackPage removes the record for a given PA. Called from bottom-half
// (normal Go context, not nosplit) — the deferred-untrack drain
// (ProcessDeferredRecords) is the only caller; never call this directly
// from //go:nosplit or IRQ-off code (see deferred.go's doc comment).
//
// O(1): trackerIdxSlotPtr resolves pa to its slot directly instead of
// scanning pageTracker (MAZ-163 — the O(n) scan under trackerLock was fine
// at the old single-caller call rate but is the wrong shape once every
// BuddyFreeTyped enqueues an untrack).
func UntrackPage(pa uintptr) {
	trackerLock.Lock()
	slot := trackerIdxSlotPtr(pa)
	if slot == nil || *slot == 0 {
		trackerLock.Unlock()
		return // Not tracked (e.g. a pinned/shared page, or already untracked)
	}
	i := int(*slot - 1)

	// Swap with the last live entry, then fix up the index for whatever
	// record just moved into slot i (unless i was already the last slot).
	trackerCount--
	moved := pageTracker[trackerCount]
	pageTracker[i] = moved
	if i != trackerCount {
		if movedSlot := trackerIdxSlotPtr(moved.PA); movedSlot != nil {
			*movedSlot = uint32(i) + 1
		}
	}
	*slot = 0
	trackerLock.Unlock()
}

// MaxShepherds is the maximum number of shepherds (processes) we track.
const MaxShepherds = 16

// MemoryStats summarizes page tracker state by type and owner.
type MemoryStats struct {
	KernelHeapPages uint64
	KernelPTPages   uint64
	UserPages       uint64
	UserPTPages     uint64
	FileBufferPages uint64
	TotalTracked    uint64
	// Per-shepherd page counts (index = shepherdID directly; [0] = kernel)
	ByShepherd [MaxShepherds]uint64
}

// shepherdIndex maps a ShepherdID to an array index.
// PID is used directly as index; out-of-range maps to 0 (kernel).
func shepherdIndex(pid int16) int {
	idx := int(pid)
	if idx < 0 || idx >= MaxShepherds {
		return 0 // treat out-of-range as kernel
	}
	return idx
}

// LogMemoryStats is the GetMemoryStats() consumer (MAZ-163 — the subsystem
// was previously write-only: five QueueDeferredRecord sites feed it on
// every page allocation, and nothing ever read it back). Logs the per-type
// breakdown plus the deferred-queue overflow count, since a dropped
// untrack record silently reintroduces the tracker-full leak this ticket
// fixes. Called from pageAuditBottomHalf (kmazarin/kmazarin/bottom_half.go)
// on the same ~30s cadence as LogPageAudit — normal Go context, not nosplit.
//
// REVIEWED AND ACCEPTED: GetMemoryStats scans every live record (up to
// MaxTrackedPages = 32768) holding trackerLock, which TrackPage/UntrackPage
// also take on the deferred-drain path. This call is what makes that
// pre-existing O(n) hold live for the first time. Keeping the scan was a
// deliberate call — at a ~30s cadence the amortized cost is negligible, and
// maintaining incremental per-type counters in TrackPage/UntrackPage would
// add bookkeeping to the hot path to save a scan that runs twice a minute.
// Revisit only if the cadence tightens or MaxTrackedPages grows materially.
func LogMemoryStats() {
	stats := GetMemoryStats()
	overflows := GetDeferredOverflows()
	warnings := GetTrackerFullWarnings()
	klog.Logf("[kmem] tracker: total=%d kernelHeap=%d kernelPT=%d user=%d userPT=%d fileBuf=%d deferredOverflows=%d fullWarnings=%d\n",
		stats.TotalTracked, stats.KernelHeapPages, stats.KernelPTPages,
		stats.UserPages, stats.UserPTPages, stats.FileBufferPages,
		overflows, warnings)
}

// GetMemoryStats scans the tracker and returns per-type and per-shepherd counts.
func GetMemoryStats() MemoryStats {
	var stats MemoryStats
	trackerLock.Lock()
	stats.TotalTracked = uint64(trackerCount)
	for i := 0; i < trackerCount; i++ {
		pages := uint64(1) << uint(pageTracker[i].Order)
		switch pageTracker[i].Type {
		case PageAllocKernelHeap:
			stats.KernelHeapPages += pages
		case PageAllocKernelPT:
			stats.KernelPTPages += pages
		case PageAllocUser:
			stats.UserPages += pages
		case PageAllocUserPT:
			stats.UserPTPages += pages
		case PageAllocFileBuffer:
			stats.FileBufferPages += pages
		}
		idx := shepherdIndex(pageTracker[i].ShepherdID)
		stats.ByShepherd[idx] += pages
	}
	trackerLock.Unlock()
	return stats
}
