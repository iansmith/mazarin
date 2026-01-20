// Package idalloc provides type-safe ID allocators for kernel resources.
//
// Each allocator uses a page-aligned free stack for O(1) allocation and release.
//
// Design principles:
// - IDs are always positive (sign bit never set)
// - ID 0 is poison/invalid - never allocated
// - Page-aligned backing storage
// - Exhaustive validation with kernel panic on any error
// - Separate instance per resource type (priest, maz, thread, etc.)
//
// TODO: Shuffle the initial array before use for security/unpredictability.
// Currently disabled because RNG correctness is not yet verified.
// Once RNG is working, call Shuffle() before first allocation.

package idalloc

import (
	"kmazarin/console"
	"kmazarin/kmem"
	"unsafe"
)

const PageSize = 0x1000 // 4KB

// ============================================================================
// Int8 ID Allocator
// ============================================================================
// For 1 page: 4096 slots, but only values 1-127 usable (int8 positive, non-zero)
// Wastes slots 128-4095 due to sign bit constraint

type Int8Allocator struct {
	stack   *[PageSize]int8 // Page-aligned backing array
	sp      int             // Stack pointer (index of next ID to pop)
	max     int8            // Maximum valid ID (always <= 0x7F)
	inUse   int             // Count of IDs currently allocated
	pagePtr uintptr         // Physical address of backing page (for cleanup)
}

// NewInt8Allocator creates an ID allocator for int8 values.
// Uses exactly numPages pages of memory (must be >= 1).
// Usable IDs: 1 to min(numPages * PageSize, 127)
//
// For maz IDs: NewInt8Allocator(1) gives IDs 1-127
func NewInt8Allocator(numPages int) *Int8Allocator {
	if numPages < 1 {
		kernelPanic("Int8Allocator: numPages must be >= 1")
	}

	// Allocate page-aligned memory
	pagePtr := allocatePages(numPages)
	if pagePtr == 0 {
		kernelPanic("Int8Allocator: failed to allocate pages")
	}

	// Calculate max usable value
	// Slots available = numPages * PageSize
	// But int8 positive max = 127 (0x7F)
	// And we don't use 0
	slots := numPages * PageSize
	var max int8
	if slots > 127 {
		max = 127 // Capped by int8 positive range
	} else {
		max = int8(slots - 1) // -1 because we skip 0
	}

	if max < 1 {
		kernelPanic("Int8Allocator: no usable IDs")
	}

	a := &Int8Allocator{
		stack:   (*[PageSize]int8)(unsafe.Pointer(pagePtr)),
		sp:      0,
		max:     max,
		inUse:   0,
		pagePtr: pagePtr,
	}

	// Initialize stack with values 1 to max
	// stack[0] = max, stack[1] = max-1, ..., stack[max-1] = 1
	// This way we allocate 1, 2, 3, ... in order (pop from end)
	for i := int8(0); i < max; i++ {
		a.stack[i] = max - i // stack[0]=max, stack[1]=max-1, ...
	}
	a.sp = int(max) // Points past last valid entry

	// TODO: Shuffle array for security once RNG is verified
	// a.Shuffle()

	return a
}

// Allocate returns the next available ID, or panics if exhausted.
//
//go:nosplit
func (a *Int8Allocator) Allocate() int8 {
	// Validate state
	if a.stack == nil {
		kernelPanic("Int8Allocator.Allocate: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int8Allocator.Allocate: negative sp")
	}
	if a.sp == 0 {
		kernelPanic("Int8Allocator.Allocate: exhausted (sp=0)")
	}

	// Pop from stack
	a.sp--
	id := a.stack[a.sp]

	// Validate popped value
	if id <= 0 {
		kernelPanic("Int8Allocator.Allocate: got non-positive ID (corruption?)")
	}
	if id > a.max {
		kernelPanic("Int8Allocator.Allocate: got ID > max (corruption?)")
	}

	a.inUse++
	return id
}

// Release returns an ID to the pool.
// Panics if id is invalid or if stack would overflow.
//
//go:nosplit
func (a *Int8Allocator) Release(id int8) {
	// Validate input
	if id <= 0 {
		kernelPanic("Int8Allocator.Release: id <= 0")
	}
	if id > a.max {
		kernelPanic("Int8Allocator.Release: id > max")
	}

	// Validate state
	if a.stack == nil {
		kernelPanic("Int8Allocator.Release: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int8Allocator.Release: negative sp")
	}
	if a.sp >= int(a.max) {
		kernelPanic("Int8Allocator.Release: stack overflow (double-free?)")
	}
	if a.inUse <= 0 {
		kernelPanic("Int8Allocator.Release: inUse underflow (double-free?)")
	}

	// Push to stack
	a.stack[a.sp] = id
	a.sp++
	a.inUse--
}

// ReleaseTable zeros all entries, zeros the backing page(s), and unmaps them.
// After this call, the allocator is INVALID and must not be used.
//
// Use case: When a priest exits, release its maz ID allocator entirely.
//
//go:nosplit
func (a *Int8Allocator) ReleaseTable() {
	if a.stack == nil {
		kernelPanic("Int8Allocator.ReleaseTable: already released")
	}

	// Zero the entire backing page (security: clean memory)
	kmem.Bzero4K(a.pagePtr)

	// TODO: Unmap and free the page when we have FreeKernelFrame
	// unmapAndFreePage(a.pagePtr)

	// Mark as invalid
	a.stack = nil
	a.sp = -1 // Poison value
	a.max = 0
	a.inUse = 0
	a.pagePtr = 0
}

// Available returns the number of IDs available for allocation.
//
//go:nosplit
func (a *Int8Allocator) Available() int {
	return a.sp
}

// InUse returns the number of IDs currently allocated.
//
//go:nosplit
func (a *Int8Allocator) InUse() int {
	return a.inUse
}

// Max returns the maximum valid ID value.
//
//go:nosplit
func (a *Int8Allocator) Max() int8 {
	return a.max
}

// ============================================================================
// Int16 ID Allocator
// ============================================================================
// For 1 page: 2048 slots (4096/2), values 1-2047 usable
// For 16 pages: 32768 slots, but only 1-32767 usable (int16 positive max)

type Int16Allocator struct {
	stack   []int16 // Backing array (page-aligned)
	sp      int     // Stack pointer
	max     int16   // Maximum valid ID (always <= 0x7FFF)
	inUse   int     // Count of IDs currently allocated
	pagePtr uintptr // Physical address of backing pages
}

// NewInt16Allocator creates an ID allocator for int16 values.
// Uses exactly numPages pages of memory.
// Usable IDs: 1 to min(numPages * PageSize / 2, 32767)
//
// For priest IDs: NewInt16Allocator(1) gives IDs 1-2047
// For larger pools: NewInt16Allocator(16) gives IDs 1-32767
func NewInt16Allocator(numPages int) *Int16Allocator {
	if numPages < 1 {
		kernelPanic("Int16Allocator: numPages must be >= 1")
	}

	// Allocate page-aligned memory
	pagePtr := allocatePages(numPages)
	if pagePtr == 0 {
		kernelPanic("Int16Allocator: failed to allocate pages")
	}

	// Calculate max usable value
	slots := (numPages * PageSize) / 2 // 2 bytes per int16
	var max int16
	if slots > 0x7FFF {
		max = 0x7FFF // Capped by int16 positive range (32767)
	} else {
		max = int16(slots - 1) // -1 because we skip 0
	}

	if max < 1 {
		kernelPanic("Int16Allocator: no usable IDs")
	}

	// Create slice header pointing to page-aligned memory
	slicePtr := (*[1 << 20]int16)(unsafe.Pointer(pagePtr)) // Large enough
	stack := slicePtr[:max]                                // Slice to actual size needed

	a := &Int16Allocator{
		stack:   stack,
		sp:      0,
		max:     max,
		inUse:   0,
		pagePtr: pagePtr,
	}

	// Initialize stack with values 1 to max
	for i := int16(0); i < max; i++ {
		a.stack[i] = max - i
	}
	a.sp = int(max)

	// TODO: Shuffle array for security once RNG is verified
	// a.Shuffle()

	return a
}

// Allocate returns the next available ID, or panics if exhausted.
//
//go:nosplit
func (a *Int16Allocator) Allocate() int16 {
	if a.stack == nil {
		kernelPanic("Int16Allocator.Allocate: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int16Allocator.Allocate: negative sp")
	}
	if a.sp == 0 {
		kernelPanic("Int16Allocator.Allocate: exhausted (sp=0)")
	}

	a.sp--
	id := a.stack[a.sp]

	if id <= 0 {
		kernelPanic("Int16Allocator.Allocate: got non-positive ID")
	}
	if id > a.max {
		kernelPanic("Int16Allocator.Allocate: got ID > max")
	}

	a.inUse++
	return id
}

// Release returns an ID to the pool.
//
//go:nosplit
func (a *Int16Allocator) Release(id int16) {
	if id <= 0 {
		kernelPanic("Int16Allocator.Release: id <= 0")
	}
	if id > a.max {
		kernelPanic("Int16Allocator.Release: id > max")
	}
	if a.stack == nil {
		kernelPanic("Int16Allocator.Release: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int16Allocator.Release: negative sp")
	}
	if a.sp >= int(a.max) {
		kernelPanic("Int16Allocator.Release: stack overflow")
	}
	if a.inUse <= 0 {
		kernelPanic("Int16Allocator.Release: inUse underflow")
	}

	a.stack[a.sp] = id
	a.sp++
	a.inUse--
}

// ReleaseTable zeros all entries, zeros the backing page(s), and unmaps them.
// After this call, the allocator is INVALID and must not be used.
//
//go:nosplit
func (a *Int16Allocator) ReleaseTable() {
	if a.stack == nil {
		kernelPanic("Int16Allocator.ReleaseTable: already released")
	}

	// Calculate number of pages
	numPages := (int(a.max)*2 + PageSize - 1) / PageSize

	// Zero all backing pages
	for i := 0; i < numPages; i++ {
		kmem.Bzero4K(a.pagePtr + uintptr(i*PageSize))
	}

	// TODO: Unmap and free pages when we have FreeKernelFrame

	// Mark as invalid
	a.stack = nil
	a.sp = -1
	a.max = 0
	a.inUse = 0
	a.pagePtr = 0
}

// Available returns the number of IDs available.
//
//go:nosplit
func (a *Int16Allocator) Available() int { return a.sp }

// InUse returns the number of IDs allocated.
//
//go:nosplit
func (a *Int16Allocator) InUse() int { return a.inUse }

// Max returns the maximum valid ID.
//
//go:nosplit
func (a *Int16Allocator) Max() int16 { return a.max }

// ============================================================================
// Int32 ID Allocator (same pattern)
// ============================================================================

type Int32Allocator struct {
	stack   []int32
	sp      int
	max     int32
	inUse   int
	pagePtr uintptr
}

// NewInt32Allocator creates an ID allocator for int32 values.
// Usable IDs: 1 to min(numPages * PageSize / 4, 0x7FFFFFFF)
func NewInt32Allocator(numPages int) *Int32Allocator {
	if numPages < 1 {
		kernelPanic("Int32Allocator: numPages must be >= 1")
	}

	pagePtr := allocatePages(numPages)
	if pagePtr == 0 {
		kernelPanic("Int32Allocator: failed to allocate pages")
	}

	slots := (numPages * PageSize) / 4
	var max int32
	if slots > 0x7FFFFFFF {
		max = 0x7FFFFFFF
	} else {
		max = int32(slots - 1)
	}

	if max < 1 {
		kernelPanic("Int32Allocator: no usable IDs")
	}

	slicePtr := (*[1 << 20]int32)(unsafe.Pointer(pagePtr))
	stack := slicePtr[:max]

	a := &Int32Allocator{
		stack:   stack,
		sp:      0,
		max:     max,
		inUse:   0,
		pagePtr: pagePtr,
	}

	for i := int32(0); i < max; i++ {
		a.stack[i] = max - i
	}
	a.sp = int(max)

	return a
}

// Allocate returns the next available ID, or panics if exhausted.
//
//go:nosplit
func (a *Int32Allocator) Allocate() int32 {
	if a.stack == nil {
		kernelPanic("Int32Allocator.Allocate: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int32Allocator.Allocate: negative sp")
	}
	if a.sp == 0 {
		kernelPanic("Int32Allocator.Allocate: exhausted (sp=0)")
	}

	a.sp--
	id := a.stack[a.sp]

	if id <= 0 {
		kernelPanic("Int32Allocator.Allocate: got non-positive ID")
	}
	if id > a.max {
		kernelPanic("Int32Allocator.Allocate: got ID > max")
	}

	a.inUse++
	return id
}

// Release returns an ID to the pool.
//
//go:nosplit
func (a *Int32Allocator) Release(id int32) {
	if id <= 0 {
		kernelPanic("Int32Allocator.Release: id <= 0")
	}
	if id > a.max {
		kernelPanic("Int32Allocator.Release: id > max")
	}
	if a.stack == nil {
		kernelPanic("Int32Allocator.Release: nil stack")
	}
	if a.sp < 0 {
		kernelPanic("Int32Allocator.Release: negative sp")
	}
	if a.sp >= int(a.max) {
		kernelPanic("Int32Allocator.Release: stack overflow")
	}
	if a.inUse <= 0 {
		kernelPanic("Int32Allocator.Release: inUse underflow")
	}

	a.stack[a.sp] = id
	a.sp++
	a.inUse--
}

// ReleaseTable zeros all entries and backing pages.
//
//go:nosplit
func (a *Int32Allocator) ReleaseTable() {
	if a.stack == nil {
		kernelPanic("Int32Allocator.ReleaseTable: already released")
	}

	numPages := (int(a.max)*4 + PageSize - 1) / PageSize

	for i := 0; i < numPages; i++ {
		kmem.Bzero4K(a.pagePtr + uintptr(i*PageSize))
	}

	a.stack = nil
	a.sp = -1
	a.max = 0
	a.inUse = 0
	a.pagePtr = 0
}

// Available returns the number of IDs available.
//
//go:nosplit
func (a *Int32Allocator) Available() int { return a.sp }

// InUse returns the number of IDs allocated.
//
//go:nosplit
func (a *Int32Allocator) InUse() int { return a.inUse }

// Max returns the maximum valid ID.
//
//go:nosplit
func (a *Int32Allocator) Max() int32 { return a.max }

// ============================================================================
// Helper Functions
// ============================================================================

// allocatePages allocates numPages of page-aligned, zeroed memory.
// Returns the virtual address of the first page, or 0 on failure.
//
//go:nosplit
func allocatePages(numPages int) uintptr {
	if numPages < 1 {
		return 0
	}

	// For a kernel ID allocator, we need kernel-accessible pages
	// We allocate from the kernel frame pool and map to high memory
	cfg := kmem.GetRuntimeConfig()
	if cfg == nil {
		return 0
	}

	var firstVA uintptr
	for i := 0; i < numPages; i++ {
		framePA := kmem.AllocKernelFrame()
		if framePA == 0 {
			return 0 // OOM
		}

		// Convert PA to kernel VA
		va := framePA + uintptr(cfg.KernelVAOffset)

		// Zero the page using DC ZVA
		kmem.Bzero4K(va)

		if i == 0 {
			firstVA = va
		}
	}

	return firstVA
}

// kernelPanic triggers a kernel panic with the given message.
//
//go:nosplit
func kernelPanic(msg string) {
	console.KWriteString("\r\n*** IDALLOC PANIC: ")
	console.KWriteString(msg)
	console.KWriteString(" ***\r\n")
	// Infinite loop - proper panic handling should be added
	for {
	}
}
