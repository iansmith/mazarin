// Package vfork manages the transient→parent linkage table for in-flight
// vfork clones. Extracted from kmazarin/kmazarin/threads.go (MAZ-130) to
// make the locking logic unit-testable without the kernel runtime.
//
// All table operations hold Lock (CAS spinlock) before accessing Table.
// Lock ordering: Lock is always acquired INSIDE schedulerLock (never the
// reverse), so callers already holding schedulerLock may safely acquire Lock.
package vfork

import (
	"sync/atomic"

	"mazzy/kmazarin/proc"
)

// Entry records the relationship between a transient child thread (spawned
// by a vfork clone) and its suspended parent thread.
//
// Protocol: Record inserts with TransientID=0 (placeholder);
// BackfillTransientID fills in TransientID once the TID is known. Lookup
// and Clear operate on entries with a non-zero TransientID.
type Entry struct {
	TransientID int16           // child transient TID (0 = reserved but not yet assigned)
	ParentID    int16           // parent thread TID to wake on exec or failure
	ReservedPID proc.ShepherdId // child PID reserved at clone time
}

// Table holds up to 16 concurrent vfork linkages. Fixed-size, no heap,
// nosplit-safe.
var Table [16]Entry

// Lock guards all Table accesses. Acquired via CAS(0→1); released via store(0).
var Lock uint32

// Record inserts a new linkage entry with TransientID=0 (the transient TID is
// not yet known at record time; BackfillTransientID fills it in later).
// Returns true on success, false if the table is full (>16 concurrent vforks).
//
//go:nosplit
func Record(parentID int16, pid proc.ShepherdId) bool {
	for !atomic.CompareAndSwapUint32(&Lock, 0, 1) {
	}
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == 0 {
			Table[i].ParentID = parentID
			Table[i].ReservedPID = pid
			atomic.StoreUint32(&Lock, 0)
			return true
		}
	}
	atomic.StoreUint32(&Lock, 0)
	return false
}

// BackfillTransientID fills in the transient TID for the placeholder entry
// whose ParentID matches parentID. Called once the transient TID is allocated.
//
//go:nosplit
func BackfillTransientID(parentID, transientID int16) {
	for !atomic.CompareAndSwapUint32(&Lock, 0, 1) {
	}
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == parentID {
			Table[i].TransientID = transientID
			atomic.StoreUint32(&Lock, 0)
			return
		}
	}
	atomic.StoreUint32(&Lock, 0)
}

// ClearByParent removes the placeholder entry for parentID (used on error
// paths before BackfillTransientID has been called).
//
//go:nosplit
func ClearByParent(parentID int16) {
	for !atomic.CompareAndSwapUint32(&Lock, 0, 1) {
	}
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == parentID {
			Table[i] = Entry{}
			atomic.StoreUint32(&Lock, 0)
			return
		}
	}
	atomic.StoreUint32(&Lock, 0)
}

// Lookup retrieves the parent TID and reserved PID for a given transient TID.
// Returns 0, 0 if not found. transientID == 0 is always a placeholder (not
// yet backfilled) and must never match a resolved lookup — early-return to
// avoid spurious hits on in-flight placeholder entries.
//
//go:nosplit
func Lookup(transientID int16) (parentID int16, pid proc.ShepherdId) {
	if transientID == 0 {
		return 0, 0
	}
	for !atomic.CompareAndSwapUint32(&Lock, 0, 1) {
	}
	for i := range Table {
		if Table[i].TransientID == transientID {
			p, pid := Table[i].ParentID, Table[i].ReservedPID
			atomic.StoreUint32(&Lock, 0)
			return p, pid
		}
	}
	atomic.StoreUint32(&Lock, 0)
	return 0, 0
}

// Clear removes the entry for transientID. Called after the parent has been
// woken (success or failure path). transientID == 0 is a placeholder — not a
// valid target for Clear (use ClearByParent instead); no-op to be safe.
//
//go:nosplit
func Clear(transientID int16) {
	if transientID == 0 {
		return
	}
	for !atomic.CompareAndSwapUint32(&Lock, 0, 1) {
	}
	for i := range Table {
		if Table[i].TransientID == transientID {
			Table[i] = Entry{}
			atomic.StoreUint32(&Lock, 0)
			return
		}
	}
	atomic.StoreUint32(&Lock, 0)
}
