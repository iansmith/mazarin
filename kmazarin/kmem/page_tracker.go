// page_tracker.go - Tracks metadata about allocated pages
//
// PageAllocInfo records are maintained in a static array. The top-half
// (page fault handler, nosplit) queues deferred records; the bottom-half
// goroutine drains the queue and inserts them here.
//
// This provides visibility into what every allocated page is used for,
// which process owns it, and what order it was allocated at.

package kmem

import "mazzy/kmazarin/serial"

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
		serial.RawUARTPuts("[kmem] WARN: page tracker full (")
		serial.RawUARTHex64(uint64(MaxTrackedPages))
		serial.RawUARTPuts(" entries)\r\n")
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

// MaxPriests is the maximum number of priests (processes) we track.
const MaxPriests = 16

// MemoryStats summarizes page tracker state by type and owner.
type MemoryStats struct {
	KernelHeapPages uint64
	KernelPTPages   uint64
	UserPages       uint64
	UserPTPages     uint64
	FileBufferPages uint64
	TotalTracked    uint64
	// Per-priest page counts (index = priestID directly; [0] = kernel)
	ByPriest [MaxPriests]uint64
}

// priestIndex maps a PriestID to an array index.
// PID is used directly as index; out-of-range maps to 0 (kernel).
func priestIndex(pid int16) int {
	idx := int(pid)
	if idx < 0 || idx >= MaxPriests {
		return 0 // treat out-of-range as kernel
	}
	return idx
}

// GetMemoryStats scans the tracker and returns per-type and per-priest counts.
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
		idx := priestIndex(pageTracker[i].PriestID)
		stats.ByPriest[idx] += pages
	}
	trackerLock.Unlock()
	return stats
}

// PrintMemoryStats prints a summary of tracked page allocations to serial.
func PrintMemoryStats() {
	stats := GetMemoryStats()
	serial.RawUARTPuts("[kmem] Page tracker: ")
	serial.RawUARTHex64(stats.TotalTracked)
	serial.RawUARTPuts(" entries\r\n")
	serial.RawUARTPuts("  Kernel heap: ")
	serial.RawUARTHex64(stats.KernelHeapPages)
	serial.RawUARTPuts(" pages\r\n")
	serial.RawUARTPuts("  Kernel PT:   ")
	serial.RawUARTHex64(stats.KernelPTPages)
	serial.RawUARTPuts(" pages\r\n")
	serial.RawUARTPuts("  User:        ")
	serial.RawUARTHex64(stats.UserPages)
	serial.RawUARTPuts(" pages\r\n")
	serial.RawUARTPuts("  User PT:     ")
	serial.RawUARTHex64(stats.UserPTPages)
	serial.RawUARTPuts(" pages\r\n")
	serial.RawUARTPuts("  File buffer: ")
	serial.RawUARTHex64(stats.FileBufferPages)
	serial.RawUARTPuts(" pages\r\n")
	// Per-priest breakdown
	serial.RawUARTPuts("  By priest:\r\n")
	// Index 0 = kernel (PID 0)
	if stats.ByPriest[0] > 0 {
		serial.RawUARTPuts("    kernel(0): ")
		serial.RawUARTHex64(stats.ByPriest[0])
		serial.RawUARTPuts(" pages\r\n")
	}
	for i := 1; i < MaxPriests; i++ {
		if stats.ByPriest[i] > 0 {
			serial.RawUARTPuts("    priest ")
			serial.RawUARTHex64(uint64(i))
			serial.RawUARTPuts(": ")
			serial.RawUARTHex64(stats.ByPriest[i])
			serial.RawUARTPuts(" pages\r\n")
		}
	}
}
