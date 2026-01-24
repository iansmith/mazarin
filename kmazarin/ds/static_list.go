//go:build arm64

package ds

// StaticList is a fixed-capacity list that STORES ELEMENTS BY VALUE.
// P is the pointer type (*Thread), E is the element type (Thread).
// Uses pointer receivers for all operations to avoid copying large structs.
// Uses boolean array to track which slots are in use.
// Methods return pointers to the stored values for read/write access.
//
// Supports reserved slots: if reserved=2, slots 0 and 1 are reserved and
// invisible to normal operations (Allocate, FindById, etc.). Access reserved
// slots via ReservedGet, ReservedSet, ReservedEmpty.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var listData [512]Thread       // Stores Thread VALUES
//	var inUseData [512]bool
//	var list = StaticList[*Thread, Thread]{Data: listData[:], InUse: inUseData[:]}
//	list.InitReserved(2)  // Optional: reserve slots 0 and 1
type StaticList[P interface {
	*E
	Ider
}, E any] struct {
	Data     []E    // Slice backed by static array - STORES VALUES
	InUse    []bool // Slice backed by static array (public, starts false = available)
	count    int    // Current number of in-use elements (excludes reserved)
	reserved int    // Number of reserved slots at the beginning (default 0)
	// NOTE: No lock - protected by external schedulerLock
}

// InitReserved sets the number of reserved slots at the beginning of the list.
// Reserved slots (0 to reserved-1) are invisible to normal operations.
// Must be called before any allocations.
//
//go:nosplit
func (l *StaticList[P, E]) InitReserved(count int) {
	if count < 0 {
		count = 0
	}
	if count > len(l.InUse) {
		count = len(l.InUse)
	}
	l.reserved = count
}

// Reserved returns the number of reserved slots.
//
//go:nosplit
func (l *StaticList[P, E]) Reserved() int {
	return l.reserved
}

// ReservedGet returns a pointer to the reserved slot at the given index.
// Index must be in range [0, reserved).
// Returns nil if index is out of range.
// Does NOT check InUse - reserved slots are always accessible.
//
//go:nosplit
func (l *StaticList[P, E]) ReservedGet(index int) P {
	if index < 0 || index >= l.reserved {
		return nil
	}
	return P(&l.Data[index])
}

// ReservedSet marks a reserved slot as in use (one-way operation).
// Once set, a reserved slot cannot be cleared back to unused.
// Index must be in range [0, reserved).
// Does nothing if index is out of range.
//
//go:nosplit
func (l *StaticList[P, E]) ReservedSet(index int) {
	if index < 0 || index >= l.reserved {
		return
	}
	l.InUse[index] = true
}

// ReservedEmpty returns true if the reserved slot at index is not in use.
// Index must be in range [0, reserved).
// Returns true if index is out of range (treat as empty).
//
//go:nosplit
func (l *StaticList[P, E]) ReservedEmpty(index int) bool {
	if index < 0 || index >= l.reserved {
		return true
	}
	return !l.InUse[index]
}

// Allocate returns pointer to next free slot and marks it in use.
// Returns (slot_index, element_pointer).
// Only considers non-reserved slots (starting from l.reserved).
// The caller should set element.TID = slot_index for proper ID tracking.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// Panics if capacity exceeded.
//go:nosplit
func (l *StaticList[P, E]) Allocate() (int, P) {
	// Debug: check if len is returning wrong value
	capacity := len(l.InUse)
	usedCount := 0
	for i := l.reserved; i < capacity; i++ {
		if l.InUse[i] {
			usedCount++
		}
	}

	// If we're about to panic, print diagnostic info first
	availableSlots := capacity - l.reserved
	if usedCount >= availableSlots {
		// Can't use console here, would cause import cycle
		// Just panic with more info
		_ = usedCount // Use the variable
	}

	for i := l.reserved; i < len(l.InUse); i++ {
		if !l.InUse[i] {
			l.InUse[i] = true
			l.count++
			return i, P(&l.Data[i])
		}
	}
	panic("StaticList exhausted: no free slots available")
}

// Contains returns true if list contains element with given ID.
// Only searches non-reserved slots.
//go:nosplit
func (l *StaticList[P, E]) Contains(id int32) bool {
	for i := l.reserved; i < len(l.InUse); i++ {
		if l.InUse[i] {
			p := P(&l.Data[i]) // Get pointer - no copy
			if p.Id() == id {  // Call on pointer receiver
				return true
			}
		}
	}
	return false
}

// FindById returns pointer to element with matching ID, or nil if not found.
// Only searches non-reserved slots.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// Implements Finder[P] interface.
//go:nosplit
func (l *StaticList[P, E]) FindById(id int32) P {
	for i := l.reserved; i < len(l.InUse); i++ {
		if l.InUse[i] {
			p := P(&l.Data[i]) // Get pointer - no copy
			if p.Id() == id {  // Call on pointer receiver
				return p
			}
		}
	}
	return nil
}

// FindByIdAll returns pointer to element with matching ID, searching ALL slots.
// Unlike FindById, this includes reserved slots.
// Use this when you need to find threads/elements that may be in reserved slots.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
//
//go:nosplit
func (l *StaticList[P, E]) FindByIdAll(id int32) P {
	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] {
			p := P(&l.Data[i]) // Get pointer - no copy
			if p.Id() == id {  // Call on pointer receiver
				return p
			}
		}
	}
	return nil
}

// ElementFor is an alias for FindById.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
//go:nosplit
func (l *StaticList[P, E]) ElementFor(id int32) P {
	return l.FindById(id)
}

// Nth returns pointer to element at index position, or nil if not in use.
// Only works for non-reserved slots (index >= reserved).
// ⚠️  WARNING: Returns pointer to LIVE internal data!
//go:nosplit
func (l *StaticList[P, E]) Nth(index int) P {
	if index < l.reserved || index >= len(l.InUse) {
		return nil
	}
	if !l.InUse[index] {
		return nil
	}
	return P(&l.Data[index])
}

// Get returns pointer to element at ANY index (including reserved slots).
// Unlike Nth, this works for both reserved and non-reserved slots.
// Use this when you have an index and need access regardless of reserved status.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// Returns nil if index out of bounds or slot not in use.
//
//go:nosplit
func (l *StaticList[P, E]) Get(index int) P {
	if index < 0 || index >= len(l.InUse) {
		return nil
	}
	if !l.InUse[index] {
		return nil
	}
	return P(&l.Data[index])
}

// Pluck returns pointer to element at index AND marks slot as free.
// Only works for non-reserved slots (index >= reserved).
// ⚠️  WARNING: Returns pointer to LIVE internal data that will be reused!
// Returns nil if index invalid or not in use.
//go:nosplit
func (l *StaticList[P, E]) Pluck(index int) P {
	if index < l.reserved || index >= len(l.InUse) {
		return nil
	}
	if !l.InUse[index] {
		return nil
	}
	l.InUse[index] = false
	l.count--
	return P(&l.Data[index])
}

// Release marks the slot at the given index as free.
// Only works for non-reserved slots (index >= reserved).
// Does nothing if index is invalid or already free.
//go:nosplit
func (l *StaticList[P, E]) Release(index int) {
	if index < l.reserved || index >= len(l.InUse) {
		return
	}
	if !l.InUse[index] {
		return // Already free
	}
	l.InUse[index] = false
	l.count--
}

// Size returns count of in-use elements.
// Implements Sizer interface.
//go:nosplit
func (l *StaticList[P, E]) Size() int {
	return l.count
}

// IndexOf returns the array index of the element with the given ID.
// Only searches non-reserved slots.
// Returns -1 if not found or ID doesn't match any in-use element.
//go:nosplit
func (l *StaticList[P, E]) IndexOf(id int32) int {
	for i := l.reserved; i < len(l.InUse); i++ {
		if l.InUse[i] {
			p := P(&l.Data[i]) // Get pointer - no copy
			if p.Id() == id {  // Call on pointer receiver
				return i
			}
		}
	}
	return -1
}

// IsEmpty returns true if no non-reserved slots are in use.
//go:nosplit
func (l *StaticList[P, E]) IsEmpty() bool {
	return l.count == 0
}

// Capacity returns the number of available (non-reserved) slots.
//go:nosplit
func (l *StaticList[P, E]) Capacity() int {
	return len(l.InUse) - l.reserved
}
