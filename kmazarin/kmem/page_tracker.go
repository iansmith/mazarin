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
	"sync/atomic"
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

	// pageIndex maps a tracked PA to its slot in pageTracker, so UntrackPage
	// (MAZ-163) resolves a PA in O(1) instead of scanning up to
	// MaxTrackedPages entries under trackerLock. Mutated only under
	// trackerLock, alongside pageTracker/trackerCount. Normal Go context
	// only (bottom-half) — never touched from nosplit/IRQ-off code.
	pageIndex = make(map[uintptr]int, MaxTrackedPages)

	// trackerFullWarnings counts how many times TrackPage has hit the
	// tracker-full branch below. Read via GetTrackerFullWarnings(); used by
	// the page_tracker_selftest (MAZ-163) to assert saturation produces zero
	// warnings. Accessed with atomics since GetTrackerFullWarnings() reads
	// it without taking trackerLock.
	trackerFullWarnings uint64
)

// TrackPage records a page allocation. Called from the bottom-half only
// (normal Go context, not nosplit).
func TrackPage(info PageAllocInfo) {
	trackerLock.Lock()
	if trackerCount < MaxTrackedPages {
		pageTracker[trackerCount] = info
		pageIndex[info.PA] = trackerCount
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
// O(1): pageIndex resolves pa to its slot directly instead of scanning
// pageTracker (MAZ-163 — the O(n) scan under trackerLock was fine at the
// old single-caller call rate but is the wrong shape once every
// BuddyFreeTyped enqueues an untrack).
func UntrackPage(pa uintptr) {
	trackerLock.Lock()
	i, ok := pageIndex[pa]
	if !ok {
		trackerLock.Unlock()
		return // Not tracked (e.g. a pinned/shared page, or already untracked)
	}
	// Swap with the last live entry, then fix up the index for whatever
	// record just moved into slot i (unless i was already the last slot).
	trackerCount--
	moved := pageTracker[trackerCount]
	pageTracker[i] = moved
	if i != trackerCount {
		pageIndex[moved.PA] = i
	}
	delete(pageIndex, pa)
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
