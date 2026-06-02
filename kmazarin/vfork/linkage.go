// Package vfork manages the transient→parent linkage table for in-flight
// vfork clones. Extracted from kmazarin/kmazarin/threads.go (MAZ-130) to
// make the locking logic unit-testable without the kernel runtime.
//
// All four table operations must hold Lock before accessing Table.
// Lock ordering: Lock is always acquired INSIDE schedulerLock (never the
// reverse), so callers holding schedulerLock may safely acquire Lock.
package vfork

import (
	"mazzy/kmazarin/proc"
)

// Entry records the relationship between a transient child thread (spawned
// by a vfork clone) and its suspended parent thread.
//
// Protocol: recordVforkParent inserts with TransientID=0 (placeholder);
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

// Lock guards all Table accesses. Acquire with CAS(0→1); release with store(0).
// Not yet wired into the accessors — MAZ-130 wires it up.
var Lock uint32

// Record inserts a new linkage entry with TransientID=0 (the transient TID is
// not yet known at record time; BackfillTransientID fills it in later).
// Returns true on success, false if the table is full (>16 concurrent vforks).
//
//go:nosplit
func Record(parentID int16, pid proc.ShepherdId) bool {
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == 0 {
			Table[i].ParentID = parentID
			Table[i].ReservedPID = pid
			return true
		}
	}
	return false
}

// BackfillTransientID fills in the transient TID for the placeholder entry
// whose ParentID matches parentID. Called once the transient TID is allocated.
//
//go:nosplit
func BackfillTransientID(parentID, transientID int16) {
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == parentID {
			Table[i].TransientID = transientID
			return
		}
	}
}

// ClearByParent removes the placeholder entry for parentID (used on error
// paths before BackfillTransientID has been called).
//
//go:nosplit
func ClearByParent(parentID int16) {
	for i := range Table {
		if Table[i].TransientID == 0 && Table[i].ParentID == parentID {
			Table[i] = Entry{}
			return
		}
	}
}

// Lookup retrieves the parent TID and reserved PID for a given transient TID.
// Returns 0, 0 if not found.
//
//go:nosplit
func Lookup(transientID int16) (parentID int16, pid proc.ShepherdId) {
	for i := range Table {
		if Table[i].TransientID == transientID {
			return Table[i].ParentID, Table[i].ReservedPID
		}
	}
	return 0, 0
}

// Clear removes the entry for transientID. Called after the parent has been
// woken (success or failure path).
//
//go:nosplit
func Clear(transientID int16) {
	for i := range Table {
		if Table[i].TransientID == transientID {
			Table[i] = Entry{}
			return
		}
	}
}
