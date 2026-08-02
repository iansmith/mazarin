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

// UntrackPage removes the record for a given PA. Called from bottom-half.
func UntrackPage(pa uintptr) {
	trackerLock.Lock()
	for i := 0; i < trackerCount; i++ {
		if pageTracker[i].PA == pa {
			// Swap with last entry
			trackerCount--
			pageTracker[i] = pageTracker[trackerCount]
			trackerLock.Unlock()
			return
		}
	}
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
