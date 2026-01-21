//go:build arm64

package ds

// StaticList is a fixed-capacity list that STORES ELEMENTS BY VALUE.
// Elements must implement Ider interface for ID-based lookup.
// Uses boolean array to track which slots are in use.
// Methods return pointers (*T) to the stored values for read/write access.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var listData [512]Thread       // Stores Thread VALUES
//	var inUseData [512]bool
//	var list = StaticList[Thread]{Data: listData[:], InUse: inUseData[:]}
type StaticList[T Ider] struct {
	Data  []T      // Slice backed by static array - STORES VALUES
	InUse []bool   // Slice backed by static array (public, starts false = available)
	count int      // Current number of in-use elements
	Lock  Spinlock // Protects all fields for concurrent access
}

// Allocate returns pointer to next free slot and marks it in use.
// Returns (slot_index, element_pointer).
// The caller should set element.TID = slot_index for proper ID tracking.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// Panics if capacity exceeded.
func (l *StaticList[T]) Allocate() (int, *T) {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	// Debug: check if len is returning wrong value
	capacity := len(l.InUse)
	usedCount := 0
	for i := 0; i < capacity; i++ {
		if l.InUse[i] {
			usedCount++
		}
	}

	// If we're about to panic, print diagnostic info first
	if usedCount >= capacity {
		// Can't use console here, would cause import cycle
		// Just panic with more info
		_ = usedCount // Use the variable
	}

	for i := 0; i < len(l.InUse); i++ {
		if !l.InUse[i] {
			l.InUse[i] = true
			l.count++
			return i, &l.Data[i]
		}
	}
	panic("StaticList exhausted: no free slots available")
}

// Contains returns true if list contains element with given ID.
func (l *StaticList[T]) Contains(id int32) bool {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] && l.Data[i].Id() == id {
			return true
		}
	}
	return false
}

// FindById returns pointer to element with matching ID, or nil if not found.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// Implements Finder[T] interface.
func (l *StaticList[T]) FindById(id int32) *T {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] && l.Data[i].Id() == id {
			return &l.Data[i]
		}
	}
	return nil
}

// ElementFor is an alias for FindById.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
func (l *StaticList[T]) ElementFor(id int32) *T {
	return l.FindById(id)
}

// Nth returns pointer to element at index position, or nil if not in use.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
func (l *StaticList[T]) Nth(index int) *T {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	if index < 0 || index >= len(l.InUse) {
		return nil
	}
	if !l.InUse[index] {
		return nil
	}
	return &l.Data[index]
}

// Pluck returns pointer to element at index AND marks slot as free.
// ⚠️  WARNING: Returns pointer to LIVE internal data that will be reused!
// Returns nil if index invalid or not in use.
func (l *StaticList[T]) Pluck(index int) *T {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	if index < 0 || index >= len(l.InUse) {
		return nil
	}
	if !l.InUse[index] {
		return nil
	}
	l.InUse[index] = false
	l.count--
	return &l.Data[index]
}

// Release marks the slot at the given index as free.
// Does nothing if index is invalid or already free.
func (l *StaticList[T]) Release(index int) {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	if index < 0 || index >= len(l.InUse) {
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
func (l *StaticList[T]) Size() int {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	return l.count
}

// IndexOf returns the array index of the element with the given ID.
// Returns -1 if not found or ID doesn't match any in-use element.
func (l *StaticList[T]) IndexOf(id int32) int {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] && l.Data[i].Id() == id {
			return i
		}
	}
	return -1
}
