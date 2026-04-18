package main

import (
	"mazzy/shared/dlist"
)

// Linux flock() operation constants.
const (
	flockSH = 1 // LOCK_SH — shared lock
	flockEX = 2 // LOCK_EX — exclusive lock
	flockNB = 4 // LOCK_NB — non-blocking
	flockUN = 8 // LOCK_UN — unlock
)

// flockEntry represents one advisory lock held by a shepherd on a file.
type flockEntry struct {
	handle uint32 // fs-side handle identifying the file
	kind   int    // flockSH or flockEX
}

// flockTable tracks all advisory locks across all shepherds, enabling
// conflict checking. Each shepherd's locks live in its own dlist.List
// (inside ShepherdFilesystemData); this table provides the global view
// needed for conflict detection.
//
// All methods are called from the single delegate-handler goroutine,
// so no locking is needed.
type flockTable struct {
	// all is every active lock across all shepherds, enabling O(n)
	// conflict scans without walking every shepherd's list.
	all *dlist.List[*flockEntry]
}

func newFlockTable() *flockTable {
	return &flockTable{
		all: dlist.New[*flockEntry](),
	}
}

// acquire attempts to place a shared or exclusive lock for the given handle.
// Returns 0 on success, negative errno on failure.
//
// If the shepherd already holds a lock on this handle, the lock is upgraded
// or downgraded in place (flock() semantics: one lock per fd per process).
func (ft *flockTable) acquire(handle uint32, kind int, nonblock bool, perShepherd *dlist.List[*flockEntry]) int64 {
	// Check for existing lock by this shepherd on the same handle.
	var existing *dlist.Node[*flockEntry]
	perShepherd.Range(func(n *dlist.Node[*flockEntry]) bool {
		if n.Value.handle == handle {
			existing = n
			return false
		}
		return true
	})

	// Check for conflicts from OTHER shepherds.
	if !ft.canAcquire(handle, kind, perShepherd) {
		if nonblock {
			return EWOULDBLOCK
		}
		// Blocking not yet implemented — return EWOULDBLOCK for now.
		// Real blocking requires a wait queue woken by release().
		return EWOULDBLOCK
	}

	if existing != nil {
		// Upgrade/downgrade in place.
		existing.Value.kind = kind
		return EOK
	}

	// New lock.
	entry := &flockEntry{handle: handle, kind: kind}
	perShepherd.PushBack(entry)
	ft.all.PushBack(entry)
	return EOK
}

// release removes the lock this shepherd holds on handle.
// Returns 0 on success, EBADF if no lock was held.
func (ft *flockTable) release(handle uint32, perShepherd *dlist.List[*flockEntry]) int64 {
	// Find in shepherd's list.
	var target *flockEntry
	var shepherdNode *dlist.Node[*flockEntry]
	perShepherd.Range(func(n *dlist.Node[*flockEntry]) bool {
		if n.Value.handle == handle {
			target = n.Value
			shepherdNode = n
			return false
		}
		return true
	})
	if target == nil {
		// No lock held — flock(LOCK_UN) on unlocked fd is not an error.
		return EOK
	}

	perShepherd.Remove(shepherdNode)
	ft.removeFromAll(target)
	return EOK
}

// releaseAll removes every lock held by a shepherd (death cleanup).
func (ft *flockTable) releaseAll(perShepherd *dlist.List[*flockEntry]) {
	perShepherd.Range(func(n *dlist.Node[*flockEntry]) bool {
		ft.removeFromAll(n.Value)
		perShepherd.Remove(n)
		return true
	})
}

// canAcquire checks whether a new lock of the given kind on handle would
// conflict with locks held by OTHER shepherds. Locks in perShepherd are
// excluded from the conflict check (same-shepherd locks are upgradeable).
func (ft *flockTable) canAcquire(handle uint32, kind int, perShepherd *dlist.List[*flockEntry]) bool {
	ok := true
	ft.all.Range(func(n *dlist.Node[*flockEntry]) bool {
		e := n.Value
		if e.handle != handle {
			return true
		}
		// Same handle — check if this lock belongs to the requesting shepherd.
		if isInList(e, perShepherd) {
			return true // own lock, not a conflict
		}
		// Lock held by another shepherd.
		if kind == flockEX || e.kind == flockEX {
			// EX conflicts with anything; SH conflicts with EX.
			ok = false
			return false
		}
		// Both SH — no conflict.
		return true
	})
	return ok
}

// removeFromAll removes the entry (by pointer identity) from the global list.
func (ft *flockTable) removeFromAll(entry *flockEntry) {
	ft.all.Range(func(n *dlist.Node[*flockEntry]) bool {
		if n.Value == entry {
			ft.all.Remove(n)
			return false
		}
		return true
	})
}

// isInList checks whether entry (by pointer identity) exists in the list.
func isInList(entry *flockEntry, list *dlist.List[*flockEntry]) bool {
	found := false
	list.Range(func(n *dlist.Node[*flockEntry]) bool {
		if n.Value == entry {
			found = true
			return false
		}
		return true
	})
	return found
}
