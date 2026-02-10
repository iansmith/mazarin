//go:build arm64 || amd64 || riscv64

package ds

// StaticOrderedList maintains two parallel arrays sorted by deadline:
// - data: thread IDs (int16)
// - orderBy: deadlines in ticks (uint64)
//
// Elements are kept sorted in ascending order by deadline.
// Insert() adds elements maintaining sort order (O(n) due to sliding).
// PopIfLess() removes first element if its deadline has expired.
//
// ZERO VALUE IS READY TO USE with backing arrays:
//
//	var dataArray [256]int16
//	var orderByArray [256]uint64
//	var list = StaticOrderedList{
//		data: dataArray[:],
//		orderBy: orderByArray[:],
//	}
type StaticOrderedList struct {
	data    []int16  // Thread IDs (parallel to orderBy)
	orderBy []uint64 // Deadlines in ticks (sorted ascending)
	size    int      // Current number of elements
}

// Init initializes a StaticOrderedList with the provided backing arrays.
// Both slices must have the same length. The list starts empty.
//
//go:nosplit
func (l *StaticOrderedList) Init(data []int16, orderBy []uint64) {
	l.data = data
	l.orderBy = orderBy
	l.size = 0
}

// Insert adds a thread with its deadline, maintaining sorted order by deadline.
// Slides existing elements to make room (O(n) operation).
// Panics if the list is full.
//
//go:nosplit
func (l *StaticOrderedList) Insert(threadId int16, deadline uint64) {
	if l.size >= len(l.data) {
		panic("StaticOrderedList.Insert: list is full")
	}

	// Find insertion point (first element with deadline > new deadline)
	insertIdx := l.size
	for i := 0; i < l.size; i++ {
		if l.orderBy[i] > deadline {
			insertIdx = i
			break
		}
	}

	// Slide elements to the right to make room
	for i := l.size; i > insertIdx; i-- {
		l.data[i] = l.data[i-1]
		l.orderBy[i] = l.orderBy[i-1]
	}

	// Insert new element
	l.data[insertIdx] = threadId
	l.orderBy[insertIdx] = deadline
	l.size++
}

// PopIfLess removes and returns the first element if its deadline <= currentTime.
// Returns (threadId, deadline) if an element was popped.
// Returns (-1, 0) if list has elements but none have expired deadlines.
// Panics if the list is empty.
//
// Typical usage:
//
//	for {
//		tid, deadline := list.PopIfLess(currentTime)
//		if tid == -1 {
//			break // No more expired deadlines
//		}
//		// Process expired deadline for thread tid
//	}
//
//go:nosplit
func (l *StaticOrderedList) PopIfLess(currentTime uint64) (int16, uint64) {
	if l.size == 0 {
		panic("StaticOrderedList.PopIfLess: list is empty")
	}

	// Check if first element's deadline has expired
	if l.orderBy[0] > currentTime {
		return -1, 0 // No expired deadlines
	}

	// Pop first element
	threadId := l.data[0]
	deadline := l.orderBy[0]

	// Slide all elements forward
	for i := 0; i < l.size-1; i++ {
		l.data[i] = l.data[i+1]
		l.orderBy[i] = l.orderBy[i+1]
	}

	l.size--
	return threadId, deadline
}

// Remove removes the first entry matching threadId from the list.
// Returns true if an entry was removed, false if not found.
//
//go:nosplit
func (l *StaticOrderedList) Remove(threadId int16) bool {
	for i := 0; i < l.size; i++ {
		if l.data[i] == threadId {
			// Slide elements left to fill the gap
			for j := i; j < l.size-1; j++ {
				l.data[j] = l.data[j+1]
				l.orderBy[j] = l.orderBy[j+1]
			}
			l.size--
			return true
		}
	}
	return false
}

// Size returns the current number of elements in the list.
//
//go:nosplit
func (l *StaticOrderedList) Size() int {
	return l.size
}

// IsEmpty returns true if the list has no elements.
//
//go:nosplit
func (l *StaticOrderedList) IsEmpty() bool {
	return l.size == 0
}
