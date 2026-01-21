//go:build arm64

package ds

// Orderer interface for elements that can be ordered by a uint64 value.
// Used by StaticOrderedList for sorting.
type Orderer interface {
	Ider                 // Must also implement Ider for ID-based lookup
	Order() uint64       // Returns the ordering value (e.g., deadline in ticks)
}

// SortOrder specifies the sort direction
type SortOrder int

const (
	Ascending  SortOrder = 0
	Descending SortOrder = 1
)

// StaticOrderedList is a fixed-capacity list that STORES ELEMENTS BY VALUE
// and keeps them sorted by their Order() value.
// Elements must implement Orderer interface (which includes Ider).
// Uses boolean array to track which slots are in use.
// Methods return pointers (*T) to the stored values for read/write access.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var listData [512]MyStruct       // Stores MyStruct VALUES
//	var inUseData [512]bool
//	var list = StaticOrderedList[MyStruct]{
//		Data: listData[:],
//		InUse: inUseData[:],
//		Order: Ascending,
//	}
type StaticOrderedList[T Orderer] struct {
	Data  []T       // Slice backed by static array - STORES VALUES
	InUse []bool    // Slice backed by static array (public, starts false = available)
	Order SortOrder // Sort order (Ascending or Descending)
	count int       // Current number of in-use elements
	Lock  Spinlock  // Protects all fields for concurrent access
}

// Allocate returns pointer to next free slot, marks it in use, and re-sorts the list.
// Returns (slot_index, element_pointer).
// The caller should initialize the element including setting its ID and Order value.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// ⚠️  WARNING: After initializing the element, caller MUST call Sort() to maintain order!
// Panics if capacity exceeded.
func (l *StaticOrderedList[T]) Allocate() (int, *T) {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	capacity := len(l.InUse)

	for i := 0; i < capacity; i++ {
		if !l.InUse[i] {
			l.InUse[i] = true
			l.count++
			// NOTE: Element is not yet initialized, so we don't sort here.
			// Caller must call Sort() after initializing the element.
			return i, &l.Data[i]
		}
	}
	panic("StaticOrderedList exhausted: no free slots available")
}

// Sort re-sorts the list according to the Order field.
// Uses insertion sort for in-place sorting with minimal allocations.
// Only sorts the in-use elements.
func (l *StaticOrderedList[T]) Sort() {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	if l.count <= 1 {
		return // Nothing to sort
	}

	// Insertion sort - good for small lists and partially sorted data
	// We sort both Data and InUse arrays together to keep them in sync
	for i := 1; i < len(l.InUse); i++ {
		if !l.InUse[i] {
			continue // Skip free slots
		}

		// Save current element
		keyData := l.Data[i]
		keyInUse := l.InUse[i]
		keyOrder := keyData.Order()

		// Find insertion position
		j := i - 1
		for j >= 0 {
			if !l.InUse[j] {
				// Skip free slots
				j--
				continue
			}

			// Compare based on sort order
			shouldMove := false
			if l.Order == Ascending {
				shouldMove = l.Data[j].Order() > keyOrder
			} else {
				shouldMove = l.Data[j].Order() < keyOrder
			}

			if !shouldMove {
				break
			}

			// Shift element right
			l.Data[j+1] = l.Data[j]
			l.InUse[j+1] = l.InUse[j]
			j--
		}

		// Insert at position j+1
		l.Data[j+1] = keyData
		l.InUse[j+1] = keyInUse
	}
}

// Contains returns true if list contains element with given ID.
func (l *StaticOrderedList[T]) Contains(id int32) bool {
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
func (l *StaticOrderedList[T]) FindById(id int32) *T {
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
func (l *StaticOrderedList[T]) ElementFor(id int32) *T {
	return l.FindById(id)
}

// Nth returns pointer to element at index position, or nil if not in use.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
// ⚠️  NOTE: Index is the position in the array, NOT the sorted order!
func (l *StaticOrderedList[T]) Nth(index int) *T {
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

// NthSorted returns pointer to the Nth element in sorted order.
// index=0 returns the first element (smallest if Ascending, largest if Descending).
// Returns nil if index is out of bounds or there aren't that many in-use elements.
// ⚠️  WARNING: This is O(n) - scans from the beginning to find the Nth in-use element.
func (l *StaticOrderedList[T]) NthSorted(index int) *T {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	if index < 0 {
		return nil
	}

	count := 0
	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] {
			if count == index {
				return &l.Data[i]
			}
			count++
		}
	}
	return nil
}

// First returns pointer to the first element in sorted order, or nil if empty.
// ⚠️  WARNING: Returns pointer to LIVE internal data!
func (l *StaticOrderedList[T]) First() *T {
	return l.NthSorted(0)
}

// PopFirst removes and returns the first element in sorted order.
// Returns nil if list is empty.
// ⚠️  WARNING: Returns pointer to LIVE internal data that will be reused!
// Caller should copy any needed data before the slot is reallocated.
func (l *StaticOrderedList[T]) PopFirst() *T {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	for i := 0; i < len(l.InUse); i++ {
		if l.InUse[i] {
			l.InUse[i] = false
			l.count--
			return &l.Data[i]
		}
	}
	return nil
}

// Pluck returns pointer to element at index AND marks slot as free.
// ⚠️  WARNING: Returns pointer to LIVE internal data that will be reused!
// Returns nil if index invalid or not in use.
func (l *StaticOrderedList[T]) Pluck(index int) *T {
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
func (l *StaticOrderedList[T]) Release(index int) {
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
func (l *StaticOrderedList[T]) Size() int {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	return l.count
}

// IsEmpty returns true if the list has no in-use elements.
func (l *StaticOrderedList[T]) IsEmpty() bool {
	l.Lock.Lock()
	defer l.Lock.Unlock()

	return l.count == 0
}
