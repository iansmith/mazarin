package vfork_test

// MAZ-130 Phase 0 — red tests for vforkParentLinkageLock being wired into
// all four table operations: Record, BackfillTransientID, Lookup, and Clear.
//
// Strategy: pre-acquire Lock (simulating a concurrent holder), then call the
// operation in a goroutine. Before the fix the operations ignore Lock and
// complete immediately; after the fix each one CAS-spins until we release.
//
// All four tests FAIL today (RED state) because Lock is declared but never
// used — all operations access Table lock-free.

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/vfork"
)

// cleanTable resets the table and lock to zero between tests.
func cleanTable(t *testing.T) {
	t.Helper()
	for i := range vfork.Table {
		vfork.Table[i] = vfork.Entry{}
	}
	atomic.StoreUint32(&vfork.Lock, 0)
}

func preLock(t *testing.T) {
	t.Helper()
	if !atomic.CompareAndSwapUint32(&vfork.Lock, 0, 1) {
		t.Fatal("vfork.Lock already held at test start — isolation broken")
	}
}

func releaseLock() {
	atomic.StoreUint32(&vfork.Lock, 0)
}

// assertBlocks verifies fn does NOT complete within window while Lock is held,
// then releases the lock and asserts fn completes promptly.
func assertBlocks(t *testing.T, name string, fn func()) {
	t.Helper()
	runtime.GOMAXPROCS(2) // spinning goroutine and unlocker must run concurrently

	done := make(chan struct{}, 1)
	go func() {
		fn()
		done <- struct{}{}
	}()

	select {
	case <-done:
		t.Errorf("%s completed while vfork.Lock was held — lock not wired in", name)
		releaseLock() // cleanup so other tests don't inherit held lock
		return
	case <-time.After(20 * time.Millisecond):
		// Good: function is blocking on the lock.
	}

	releaseLock()
	select {
	case <-done:
		// Correct: completed after lock release.
	case <-time.After(200 * time.Millisecond):
		t.Errorf("%s did not complete after vfork.Lock was released", name)
	}
}

// TestRecordRespectsLock: Record must wait for Lock before inserting.
func TestRecordRespectsLock(t *testing.T) {
	cleanTable(t)
	defer cleanTable(t)

	preLock(t)
	assertBlocks(t, "vfork.Record", func() {
		vfork.Record(int16(10), proc.ShepherdId(42))
	})
}

// TestBackfillTransientIDRespectsLock: BackfillTransientID must wait for Lock.
func TestBackfillTransientIDRespectsLock(t *testing.T) {
	cleanTable(t)
	defer cleanTable(t)

	// Insert a placeholder entry without using the lock (pre-fix, lock-free).
	vfork.Table[0] = vfork.Entry{TransientID: 0, ParentID: int16(10), ReservedPID: proc.ShepherdId(42)}

	preLock(t)
	assertBlocks(t, "vfork.BackfillTransientID", func() {
		vfork.BackfillTransientID(int16(10), int16(99))
	})
}

// TestLookupRespectsLock: Lookup must wait for Lock before reading.
func TestLookupRespectsLock(t *testing.T) {
	cleanTable(t)
	defer cleanTable(t)

	vfork.Table[0] = vfork.Entry{TransientID: int16(99), ParentID: int16(10), ReservedPID: proc.ShepherdId(42)}

	preLock(t)
	assertBlocks(t, "vfork.Lookup", func() {
		vfork.Lookup(int16(99))
	})
}

// TestClearRespectsLock: Clear must wait for Lock before modifying the table.
func TestClearRespectsLock(t *testing.T) {
	cleanTable(t)
	defer cleanTable(t)

	vfork.Table[0] = vfork.Entry{TransientID: int16(99), ParentID: int16(10), ReservedPID: proc.ShepherdId(42)}

	preLock(t)
	assertBlocks(t, "vfork.Clear", func() {
		vfork.Clear(int16(99))
	})
}

// TestClearByParentRespectsLock: ClearByParent must wait for Lock.
func TestClearByParentRespectsLock(t *testing.T) {
	cleanTable(t)
	defer cleanTable(t)

	vfork.Table[0] = vfork.Entry{TransientID: 0, ParentID: int16(10), ReservedPID: proc.ShepherdId(42)}

	preLock(t)
	assertBlocks(t, "vfork.ClearByParent", func() {
		vfork.ClearByParent(int16(10))
	})
}
