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
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

// PageDescriptor tracks the ownership and state of a single physical page.
// 24 bytes per entry (18 rounded up to pointer alignment). For a 512MB pool
// with 4KB pages = 131072 entries = 3MB.
type PageDescriptor struct {
	PA       uintptr  // Physical address of this page
	Type     PageType // What the page is used for
	Owner    int16    // 0 = kernel, 1-31 = shepherd ID
	RefCount int16    // >1 for shared pages (IPC, framebuffer)
	Order    uint8    // Buddy order (0 = single page)
	Flags    uint8    // PD_PINNED, PD_DIRTY, etc.
	MapCount int16    // MAZ-15 probe: live user leaf PTEs referencing this page. MAINTAINED ONLY under constants.Maz15Debug (mapProbeInc/Dec); deliberately NOT zeroed by ClearPageDescriptor so the free-time [MAP_LEAK] assert sees it — reset at realloc in SetPageDescriptor.
}

// PageDescriptor flag bits
const (
	PD_PINNED           = 1 << 0 // Do not evict (kernel pages, MMIO, page tables)
	PD_DIRTY            = 1 << 1 // Software dirty tracking (mirrors HW dirty)
	PD_SWAPPED          = 1 << 2 // Page is swapped out
	PD_SHARED           = 1 << 3 // Page mapped in multiple address spaces
	PD_FILE_BACKED      = 1 << 4 // File-backed mmap: dual-mapped in caller + handler; CleanupShepherdPages Phase 1 skips release so the handler can flush before freeing
	PD_NET_OWNED_SHARED = 1 << 5 // Net.elf-owned page shared into a client (per-stream send ring). Distinguishes net.elf's mappings from regular PD_SHARED so a future reclaim path can notify net.elf when the client dies; v1 relies on RefCount alone.
)

// PageShift is log2(PageSize) for PFN calculation.
const PageShift = 12

// pdEntrySize is the size of a single PageDescriptor in bytes.
// Using a const avoids calling unsafe.Sizeof in nosplit paths.
const pdEntrySize = 24 // Must match unsafe.Sizeof(PageDescriptor{})

// Compile-time assertion that pdEntrySize matches the real struct size.
const _pdRealSize = unsafe.Sizeof(PageDescriptor{})

var _ [pdEntrySize - _pdRealSize]byte
var _ [_pdRealSize - pdEntrySize]byte

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
	pageDescLock.Lock()
	if constants.Maz15Debug && desc.MapCount != 0 {
		// A freshly allocated page still shows live user PTEs from a prior
		// life: the free side either missed the accounting or genuinely freed
		// a mapped page and the [MAP_LEAK] assert was bypassed.
		klog.Criticalf("[MAP_STALE]", " pa=%x mapcount=%d newtype=%d newowner=%d\n",
			uint64(pa), int(desc.MapCount), int(pageType), int(owner))
		desc.MapCount = 0
	}
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
	pageDescLock.Unlock()
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
	pageDescLock.Lock()
	desc.PA = 0
	desc.Type = 0
	desc.Owner = 0
	desc.RefCount = 0
	desc.Order = 0
	desc.Flags = 0
	// MapCount deliberately NOT cleared — probe-maintained; see its field doc.
	pageDescLock.Unlock()
}

// pageDescLock serializes RefCount/Flags/Owner mutations on PageDescriptors
// (MAZ-15). The read-modify-write clusters ("bump + set flag", "dec + maybe
// clear flag", "dec-to-zero + clear + free") run from two context classes:
// IRQ-masked syscall handlers AND preemptible thread-0 cleanup
// (DrainDeferredCleanups → CleanupShepherdPages). Without a shared IRQ-atomic
// lock, thread-0's plain int16 RMW can be preempted mid-update by a syscall's
// bump on the same descriptor — the lost increment frees a page that is still
// mapped elsewhere, and the next owner of that page gets its heap scribbled.
// Lock order: the buddy lock may be held when taking pageDescLock
// (SetPageDescriptor from BuddyAllocTyped); NEVER take the buddy lock while
// holding pageDescLock — release it first (see releasePageByPA).
var pageDescLock Spinlock

// RefSharedPage bumps RefCount and sets flags as one atomic cluster.
//
//go:nosplit
func RefSharedPage(desc *PageDescriptor, flags uint8) {
	pageDescLock.Lock()
	desc.RefCount++
	desc.Flags |= flags
	pageDescLock.Unlock()
}

// UnrefSharedPage decrements RefCount and clears flags once the page is back
// to owner-only (RefCount <= 1), as one atomic cluster.
//
//go:nosplit
func UnrefSharedPage(desc *PageDescriptor, flags uint8) {
	pageDescLock.Lock()
	desc.RefCount--
	if desc.RefCount <= 1 {
		desc.Flags &^= flags
	}
	pageDescLock.Unlock()
}

// SetPageDescriptorFlags ORs flags into the descriptor under the lock.
//
//go:nosplit
func SetPageDescriptorFlags(desc *PageDescriptor, flags uint8) {
	pageDescLock.Lock()
	desc.Flags |= flags
	pageDescLock.Unlock()
}

// ClearPageDescriptorFlags clears flags on the descriptor under the lock.
//
//go:nosplit
func ClearPageDescriptorFlags(desc *PageDescriptor, flags uint8) {
	pageDescLock.Lock()
	desc.Flags &^= flags
	pageDescLock.Unlock()
}

// RestorePageShareState overwrites RefCount+Flags from a rollback snapshot.
//
//go:nosplit
func RestorePageShareState(desc *PageDescriptor, refCount int16, flags uint8) {
	pageDescLock.Lock()
	desc.RefCount = refCount
	desc.Flags = flags
	pageDescLock.Unlock()
}

// ============================================================================
// MAZ-15 freed-while-mapped probe (gated by constants.Maz15Debug)
// ============================================================================
//
// MapCount counts live user leaf PTEs per physical page. The invariant: a
// page must have MapCount == 0 when it returns to the buddy pool. Increment
// happens at the ONLY two leaf-PTE install sites (mapUserPageWithL0,
// MapUserDevicePageWithL0 — new-entry path only, not the already-mapped
// early return). Decrement happens at the ONLY ways a live leaf PTE dies:
// UnmapUserPage / UnmapUserPageWithL0 (explicit clear), and the
// walkAndFreePageTablePages freeLeaves walk (L3 table destroyed wholesale).
// The kernel linear map is deliberately NOT counted — it maps the whole pool
// permanently; the probe tracks user-visible mappings only.
//
// Violations report at free time ([MAP_LEAK], releasePageByPA/BuddyFreeTyped)
// and at realloc time ([MAP_STALE], SetPageDescriptor). Known-benign source:
// CleanupShepherdDMAClumps buddy-frees a dead shepherd's clump pages before
// the deferred table teardown clears its PTEs — reports with
// type=PageUserDMA + dead owner; recognize and discount in triage.

// mapProbeInc records a new live user leaf PTE for pa.
//
//go:nosplit
func mapProbeInc(pa uintptr) {
	if !constants.Maz15Debug {
		return
	}
	desc := GetPageDescriptor(pa)
	if desc == nil {
		return
	}
	pageDescLock.Lock()
	desc.MapCount++
	pageDescLock.Unlock()
}

// mapProbeDec records the death of a live user leaf PTE for pa.
//
//go:nosplit
func mapProbeDec(pa uintptr) {
	if !constants.Maz15Debug {
		return
	}
	desc := GetPageDescriptor(pa)
	if desc == nil {
		return
	}
	pageDescLock.Lock()
	desc.MapCount--
	pageDescLock.Unlock()
}

// BumpPageRefCount increments the refcount of the page at pa. Returns the new
// refcount, or 0 if pa is not in the pool. Used by the shared-text cache to
// mark a page as held by the cache itself (independent of any process mapping).
//
//go:nosplit
func BumpPageRefCount(pa uintptr) int16 {
	desc := GetPageDescriptor(pa)
	if desc == nil {
		return 0
	}
	pageDescLock.Lock()
	desc.RefCount++
	desc.Flags |= PD_SHARED
	rc := desc.RefCount
	pageDescLock.Unlock()
	return rc
}

// TransferPageOwnership atomically changes the owner of a physical page.
// Returns false if the PA is invalid or the current owner doesn't match fromPID.
//
//go:nosplit
func TransferPageOwnership(pa uintptr, fromPID, toPID int16) bool {
	desc := GetPageDescriptor(pa)
	if desc == nil {
		return false
	}
	pageDescLock.Lock()
	if desc.Owner != fromPID {
		pageDescLock.Unlock()
		return false
	}
	desc.Owner = toPID
	pageDescLock.Unlock()
	return true
}

// MaxOwners is the maximum number of shepherd owners tracked (0=kernel, 1-31=SIDs).
const MaxOwners = 32

// PagesByOwner counts allocated pages for each owner SID.
// Index 0 = kernel, 1-31 = shepherd SID. Returns array by value.
// O(pdCapacity) — only call from OOM or diagnostic paths.
//
//go:nosplit
func PagesByOwner() [MaxOwners]uint64 {
	var counts [MaxOwners]uint64
	if atomic.LoadUint32(&pdInitialized) == 0 {
		return counts
	}
	cap := pdCapacity
	for i := uintptr(0); i < uintptr(cap); i++ {
		desc := pdAt(i)
		if desc.Type == 0 {
			continue
		}
		owner := int(desc.Owner)
		if owner < 0 || owner >= MaxOwners {
			owner = 0
		}
		counts[owner]++
	}
	return counts
}

// LogPagesByOwnerUART prints per-owner page counts via UART. No heap allocation.
//
//go:nosplit
func LogPagesByOwnerUART() {
	counts := PagesByOwner()
	serial.RawUARTPuts("[kmem] per-SID pages:\r\n")
	for i := 0; i < MaxOwners; i++ {
		if counts[i] == 0 {
			continue
		}
		serial.RawUARTPuts("[kmem]   SID=")
		SerialHex16(uint64(i))
		serial.RawUARTPuts(": ")
		SerialHex16(counts[i])
		serial.RawUARTPuts(" pages (")
		SerialHex16(counts[i] * 4)
		serial.RawUARTPuts(" KB)\r\n")
	}
}

// LogPageAudit prints a full audit of physical page descriptor state via klog.
// Shows per-type counts, which types have pinned pages, dirty-page count, shared
// pages (RefCount > 1), and per-owner totals. O(pdCapacity) — diagnostics only.
func LogPageAudit() {
	if atomic.LoadUint32(&pdInitialized) == 0 {
		klog.Logf("[kmem] audit: page descriptors not initialized\n")
		return
	}

	var countByType [PageTypeCount]uint64
	var pinnedByType [PageTypeCount]uint64
	var dirtyCount uint64
	var sharedCount uint64
	var totalInUse uint64

	cap := pdCapacity
	for i := uintptr(0); i < uintptr(cap); i++ {
		desc := pdAt(i)
		if desc.Type == 0 {
			continue
		}
		totalInUse++
		t := desc.Type
		if t < PageTypeCount {
			countByType[t]++
			if desc.Flags&PD_PINNED != 0 {
				pinnedByType[t]++
			}
		}
		if desc.Flags&PD_DIRTY != 0 {
			dirtyCount++
		}
		if desc.RefCount > 1 {
			sharedCount++
		}
	}

	counts := PagesByOwner()

	// Fingerprint the audit so a quiescent system (e.g. wedged after a crash)
	// doesn't re-dump an identical breakdown every ~30s — collapse a repeat of
	// the previous audit to a single "unchanged" heartbeat line.
	const fnvOffset = 14695981039346656037
	const fnvPrime = 1099511628211
	fp := uint64(fnvOffset)
	mix := func(v uint64) { fp = (fp ^ v) * fnvPrime }
	mix(totalInUse)
	mix(dirtyCount)
	mix(sharedCount)
	for i := PageType(0); i < PageTypeCount; i++ {
		mix(countByType[i])
		mix(pinnedByType[i])
	}
	for i := 0; i < MaxOwners; i++ {
		mix(counts[i])
	}
	if lastAuditValid && fp == lastAuditFingerprint {
		klog.Logf("[kmem] page audit unchanged: in-use=%d\n", totalInUse)
		return
	}
	lastAuditFingerprint = fp
	lastAuditValid = true

	klog.Logf("[kmem] === page audit === in-use: %d\n", totalInUse)
	for i := PageType(0); i < PageTypeCount; i++ {
		if countByType[i] == 0 {
			continue
		}
		if pinnedByType[i] > 0 {
			klog.Logf("[kmem]   %s: %d pages (%d KiB) [pinned=%d]\n",
				pageTypeNames[i], countByType[i], countByType[i]*4, pinnedByType[i])
		} else {
			klog.Logf("[kmem]   %s: %d pages (%d KiB)\n",
				pageTypeNames[i], countByType[i], countByType[i]*4)
		}
	}
	if dirtyCount > 0 {
		klog.Logf("[kmem]   dirty: %d pages\n", dirtyCount)
	}
	if sharedCount > 0 {
		klog.Logf("[kmem]   shared (RefCount>1): %d pages\n", sharedCount)
	}

	for i := 0; i < MaxOwners; i++ {
		if counts[i] == 0 {
			continue
		}
		klog.Logf("[kmem]   SID=%d: %d pages (%d KiB)\n", i, counts[i], counts[i]*4)
	}
	klog.Logf("[kmem] === end page audit ===\n")
}

// Previous page-audit fingerprint, used to suppress identical consecutive
// dumps. Touched only from the page-audit bottom-half goroutine (serialized
// via pageAuditChan), so no synchronization is required.
var (
	lastAuditFingerprint uint64
	lastAuditValid       bool
)
