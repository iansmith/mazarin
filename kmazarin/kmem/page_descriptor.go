// page_descriptor.go - Per-frame metadata array (Linux struct page equivalent)
//
// PageDescriptor tracks the ownership and state of every allocated physical page.
// Stored in a flat array indexed by (PA - poolStart) / PageSize (the PFN).
// This is O(1) lookup given any PA in the pool.
//
// Bootstrap: the array is allocated from the bump allocator before the buddy
// allocator is initialized. The array itself is marked as pinned kernel memory.
//
// CRITICAL: No Go maps. No Go slices. All data structures are flat arrays
// accessed via raw pointer arithmetic. This avoids GC write barriers that
// would blow the nosplit stack budget.

package kmem

import (
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// PageDescriptor tracks the ownership and state of a single physical page.
// 16 bytes per entry. For a 512MB pool with 4KB pages = 131072 entries = 2MB.
type PageDescriptor struct {
	PA       uintptr  // Physical address of this page
	Type     PageType // What the page is used for
	Owner    int16    // 0 = kernel, 1-31 = priest ID
	RefCount int16    // >1 for shared pages (IPC, framebuffer)
	Order    uint8    // Buddy order (0 = single page)
	Flags    uint8    // PD_PINNED, PD_DIRTY, etc.
}

// PageDescriptor flag bits
const (
	PD_PINNED  = 1 << 0 // Do not evict (kernel pages, MMIO, page tables)
	PD_DIRTY   = 1 << 1 // Software dirty tracking (mirrors HW dirty)
	PD_SWAPPED = 1 << 2 // Page is swapped out
	PD_SHARED  = 1 << 3 // Page mapped in multiple address spaces
)

// PageShift is log2(PageSize) for PFN calculation.
const PageShift = 12

// pdEntrySize is the size of a single PageDescriptor in bytes.
// Using a const avoids calling unsafe.Sizeof in nosplit paths.
const pdEntrySize = 16 // Must match unsafe.Sizeof(PageDescriptor{})

// Descriptor array state — NO Go slice, just raw uintptr + length.
// This avoids GC write barriers that blow the nosplit stack budget.
var (
	pdBase        uintptr // VA of the PageDescriptor array
	pdPoolStart   uintptr // Start of pool (PA) for PFN calculation
	pdPoolEnd     uintptr // End of pool (PA)
	pdInitialized uint32  // Atomic: 1 = ready
	pdCapacity    uint64  // Total entries in the array
)

// pdAt returns a pointer to the PageDescriptor at the given index.
// No bounds checking — caller must verify idx < pdCapacity.
//
//go:nosplit
func pdAt(idx uintptr) *PageDescriptor {
	return (*PageDescriptor)(unsafe.Pointer(pdBase + idx*pdEntrySize))
}

// InitPageDescriptors allocates and initializes the PageDescriptor array.
// Must be called from InitUnifiedPool BEFORE the buddy allocator is initialized.
// Uses the bump allocator to carve out space from the pool.
//
// The array covers the entire unified pool: one entry per 4KB page.
//
//go:nosplit
func InitPageDescriptors(poolStart, poolEnd uintptr) {
	if atomic.LoadUint32(&pdInitialized) != 0 {
		return
	}

	pdPoolStart = poolStart
	pdPoolEnd = poolEnd

	totalPages := uint64(poolEnd-poolStart) / PageSize
	pdCapacity = totalPages

	arraySize := uintptr(totalPages) * pdEntrySize

	// Round up to page boundary
	arrayPages := (arraySize + PageSize - 1) / PageSize

	serial.RawUARTPuts("[kmem] PageDescriptor array: ")
	serial.RawUARTHex64(totalPages)
	serial.RawUARTPuts(" entries (")
	serial.RawUARTHex64(uint64(arrayPages))
	serial.RawUARTPuts(" pages)\r\n")

	// Bump-allocate from the pool
	globalPool.lock.Lock()
	allocSize := arrayPages * PageSize
	if globalPool.next+allocSize > globalPool.end {
		globalPool.lock.Unlock()
		serial.RawUARTPuts("[kmem] FATAL: pool too small for PageDescriptor array!\r\n")
		for {
		}
	}

	arrayPA := globalPool.next
	globalPool.next += allocSize
	globalPool.lock.Unlock()

	// Map to kernel VA and zero
	arrayVA := paToVA(arrayPA)
	for i := uintptr(0); i < arrayPages; i++ {
		Bzero4K(arrayVA + i*PageSize)
	}

	// Store raw base pointer — NO Go slice, NO unsafe.Slice
	pdBase = arrayVA

	atomic.StoreUint32(&pdInitialized, 1)
}

// SetPageDescriptor sets the descriptor for a page at the given PA.
// Called from BuddyAllocTyped after allocation.
//
//go:nosplit
func SetPageDescriptor(pa uintptr, pageType PageType, owner int16, order uint8) {
	if atomic.LoadUint32(&pdInitialized) == 0 {
		return
	}
	idx := (pa - pdPoolStart) >> PageShift
	if idx >= uintptr(pdCapacity) {
		return // Out of pool range
	}
	desc := pdAt(idx)
	desc.PA = pa
	desc.Type = pageType
	desc.Owner = owner
	desc.RefCount = 1
	desc.Order = order
	desc.Flags = 0
	// Pin kernel pages by default
	if pageType.IsKernelType() {
		desc.Flags = PD_PINNED
	}
}

// GetPageDescriptor returns a pointer to the PageDescriptor for a given PA.
// Returns nil if the PA is outside the pool or descriptors not initialized.
//
//go:nosplit
func GetPageDescriptor(pa uintptr) *PageDescriptor {
	if atomic.LoadUint32(&pdInitialized) == 0 {
		return nil
	}
	if pa < pdPoolStart || pa >= pdPoolEnd {
		return nil
	}
	idx := (pa - pdPoolStart) >> PageShift
	if idx >= uintptr(pdCapacity) {
		return nil
	}
	return pdAt(idx)
}

// ClearPageDescriptor zeros the descriptor for a page.
// Called when a page is freed back to the buddy allocator.
//
//go:nosplit
func ClearPageDescriptor(pa uintptr) {
	if atomic.LoadUint32(&pdInitialized) == 0 {
		return
	}
	idx := (pa - pdPoolStart) >> PageShift
	if idx >= uintptr(pdCapacity) {
		return
	}
	desc := pdAt(idx)
	desc.PA = 0
	desc.Type = 0
	desc.Owner = 0
	desc.RefCount = 0
	desc.Order = 0
	desc.Flags = 0
}

// TransferPageOwnership atomically changes the owner of a physical page.
// Returns false if the PA is invalid or the current owner doesn't match fromPID.
//
//go:nosplit
func TransferPageOwnership(pa uintptr, fromPID, toPID int16) bool {
	desc := GetPageDescriptor(pa)
	if desc == nil || desc.Owner != fromPID {
		return false
	}
	desc.Owner = toPID
	return true
}

// PrintPageStats walks the PageDescriptor array and prints per-type and
// per-priest page counts. This is a diagnostic function — the linear scan
// over the array costs microseconds and should only be called on demand.
func PrintPageStats() {
	if atomic.LoadUint32(&pdInitialized) == 0 {
		serial.RawUARTPuts("[kmem] PageDescriptors not initialized\r\n")
		return
	}

	// Count by type
	var typeCounts [PageTypeCount]uint64
	var priestCounts [32]uint64 // PriestId 0-31
	var totalAllocated uint64

	for i := uint64(0); i < pdCapacity; i++ {
		desc := pdAt(uintptr(i))
		if desc.RefCount <= 0 {
			continue // Free or untracked
		}
		totalAllocated++
		if desc.Type < PageTypeCount {
			typeCounts[desc.Type]++
		}
		pidIdx := desc.Owner
		if pidIdx < 0 {
			pidIdx = 0
		}
		if pidIdx < 32 {
			priestCounts[pidIdx]++
		}
	}

	serial.RawUARTPuts("[kmem] Page Stats (")
	serial.RawUARTHex64(totalAllocated)
	serial.RawUARTPuts(" allocated / ")
	serial.RawUARTHex64(pdCapacity)
	serial.RawUARTPuts(" total):\r\n")

	// Print per-type
	for t := PageType(0); t < PageTypeCount; t++ {
		if typeCounts[t] == 0 {
			continue
		}
		serial.RawUARTPuts("  ")
		name := t.String()
		for j := 0; j < len(name); j++ {
			serial.PollWrite(name[j])
		}
		serial.RawUARTPuts(": ")
		serial.RawUARTHex64(typeCounts[t])
		serial.RawUARTPuts(" pages\r\n")
	}

	// Print per-priest
	serial.RawUARTPuts("  By priest:\r\n")
	if priestCounts[0] > 0 {
		serial.RawUARTPuts("    kernel(0): ")
		serial.RawUARTHex64(priestCounts[0])
		serial.RawUARTPuts("\r\n")
	}
	for i := 1; i < 32; i++ {
		if priestCounts[i] > 0 {
			serial.RawUARTPuts("    priest ")
			serial.RawUARTHex64(uint64(i))
			serial.RawUARTPuts(": ")
			serial.RawUARTHex64(priestCounts[i])
			serial.RawUARTPuts("\r\n")
		}
	}
}
