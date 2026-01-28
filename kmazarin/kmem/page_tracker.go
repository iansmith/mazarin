// page_tracker.go - Tracks metadata about allocated pages
//
// PageAllocInfo records are maintained in a static array. The top-half
// (page fault handler, nosplit) queues deferred records; the bottom-half
// goroutine drains the queue and inserts them here.
//
// This provides visibility into what every allocated page is used for,
// which process owns it, and what order it was allocated at.

package kmem

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
	PA       uintptr       // Physical address of the page
	VA       uintptr       // Virtual address (0 if not mapped to user VA)
	Type     PageAllocType // Purpose of the allocation
	PriestID int16         // Process ID (-1 = kernel)
	ThreadID int16         // Thread that allocated
	Order    uint8         // Buddy order (0 = single page)
}

// MaxTrackedPages is the capacity of the page tracker array.
// 32K entries * 24 bytes = ~768KB. Covers up to 128MB of 4KB pages.
const MaxTrackedPages = 32768

var (
	pageTracker [MaxTrackedPages]PageAllocInfo
	trackerCount int
	trackerLock  Spinlock
)

// TrackPage records a page allocation. Called from the bottom-half only
// (normal Go context, not nosplit).
func TrackPage(info PageAllocInfo) {
	trackerLock.Lock()
	if trackerCount < MaxTrackedPages {
		pageTracker[trackerCount] = info
		trackerCount++
	} else {
		trackerLock.Unlock()
		uartPuts("[kmem] WARN: page tracker full (")
		uartPutHex64(uint64(MaxTrackedPages))
		uartPuts(" entries)\r\n")
		return
	}
	trackerLock.Unlock()
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

// MemoryStats summarizes page tracker state by type and owner.
type MemoryStats struct {
	KernelHeapPages uint64
	KernelPTPages   uint64
	UserPages       uint64
	UserPTPages     uint64
	FileBufferPages uint64
	TotalTracked    uint64
}

// GetMemoryStats scans the tracker and returns per-type counts.
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
	}
	trackerLock.Unlock()
	return stats
}

// PrintMemoryStats prints a summary of tracked page allocations.
func PrintMemoryStats() {
	stats := GetMemoryStats()
	uartPuts("[kmem] Page tracker: ")
	uartPutHex64(stats.TotalTracked)
	uartPuts(" entries\r\n")
	uartPuts("  Kernel heap: ")
	uartPutHex64(stats.KernelHeapPages)
	uartPuts(" pages\r\n")
	uartPuts("  Kernel PT:   ")
	uartPutHex64(stats.KernelPTPages)
	uartPuts(" pages\r\n")
	uartPuts("  User:        ")
	uartPutHex64(stats.UserPages)
	uartPuts(" pages\r\n")
	uartPuts("  User PT:     ")
	uartPutHex64(stats.UserPTPages)
	uartPuts(" pages\r\n")
	uartPuts("  File buffer: ")
	uartPutHex64(stats.FileBufferPages)
	uartPuts(" pages\r\n")
}
